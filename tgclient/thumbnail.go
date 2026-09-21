package tgclient

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"telecloud/config"
	"telecloud/database"
	"telecloud/utils"

	"github.com/google/uuid"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

type TempStreamInfo struct {
	FileID   int64
	Username string
}

var TempStreamTokens sync.Map

// ffmpegSemaphore aliases the process-wide FFmpeg cap so upload-time
// thumbnails (utils.CreateLocalThumbnail) and regeneration share one budget.
var ffmpegSemaphore = utils.FFmpegSemaphore

// readerAtSeeker wraps an io.ReadSeeker to implement io.ReaderAt
type readerAtSeeker struct {
	rs io.ReadSeeker
	mu sync.Mutex
}

func (r *readerAtSeeker) ReadAt(p []byte, off int64) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.rs.Seek(off, io.SeekStart)
	if err != nil {
		return 0, err
	}
	return r.rs.Read(p)
}

func RegenerateFileThumbnail(ctx context.Context, fileID int64, cfg *config.Config) (*string, error) {
	var item database.File
	err := database.RODB.Get(&item, "SELECT id, filename, size, mime_type, is_folder, thumb_path, message_id, owner FROM files WHERE id = ?", fileID)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}

	if item.IsFolder {
		return nil, fmt.Errorf("cannot generate thumbnail for folder")
	}

	// 1. Check if the file has any message ID or parts
	hasParts := false
	var partCount int
	database.RODB.Get(&partCount, "SELECT COUNT(*) FROM file_parts WHERE file_id = ?", fileID)
	if partCount > 0 {
		hasParts = true
	}

	if item.MessageID == nil && !hasParts {
		return nil, fmt.Errorf("file has no message ID or parts on Telegram")
	}

	// 2. Determine file extension and MIME type
	rawMime := ""
	if item.MimeType != nil {
		rawMime = *item.MimeType
	}
	actualMime := utils.DetectMime(item.Filename, rawMime)
	ext := strings.ToLower(filepath.Ext(item.Filename))

	// 3. Define the output thumbnail name and path
	thumbName := strings.ReplaceAll(uuid.New().String(), "-", "") + ".jpg"
	thumbPath := filepath.Join(cfg.ThumbsDir, thumbName)
	_ = os.MkdirAll(filepath.Dir(thumbPath), 0755)

	log.Printf("[Thumbnail] Generating thumbnail for file %s (ID: %d, Mime: %s)", item.Filename, fileID, actualMime)

	// A helper to update DB and clean up the old thumbnail
	successHandler := func(newPath string) (*string, error) {
		// Verify file exists
		if _, err := os.Stat(newPath); err != nil {
			return nil, fmt.Errorf("generated thumbnail file missing: %w", err)
		}

		oldThumb := item.ThumbPath
		_, dbErr := database.DB.Exec("UPDATE files SET thumb_path = ? WHERE id = ?", newPath, fileID)
		if dbErr != nil {
			os.Remove(newPath)
			return nil, fmt.Errorf("failed to update DB: %w", dbErr)
		}

		// Delete old thumbnail if not used by other files
		if oldThumb != nil && *oldThumb != "" && *oldThumb != newPath {
			var count int
			database.RODB.Get(&count, "SELECT COUNT(*) FROM files WHERE thumb_path = ?", *oldThumb)
			if count == 0 {
				os.Remove(*oldThumb)
			}
		}

		log.Printf("[Thumbnail] Successfully generated thumbnail for file ID %d -> %s", fileID, newPath)
		return &newPath, nil
	}

	// 4. Handle EPUB / CBZ
	if ext == ".epub" || ext == ".cbz" || actualMime == "application/epub+zip" || actualMime == "application/x-cbz" {
		reader, err := GetTelegramFileReader(ctx, item, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to get file reader: %w", err)
		}
		defer reader.Close()

		ras := &readerAtSeeker{rs: reader}
		if path := extractZipCoverFromReader(ras, item.Size, thumbPath); path != nil {
			return successHandler(*path)
		}
		return nil, fmt.Errorf("failed to extract cover from zip/epub/cbz")
	}

	// 4.5. Handle PDF
	if ext == ".pdf" || actualMime == "application/pdf" {
		if errFF := generateThumbnailWithFFmpeg(ctx, fileID, actualMime, &item, cfg, thumbPath, true); errFF == nil {
			return successHandler(thumbPath)
		}
		if pdftoppmPath, err := exec.LookPath("pdftoppm"); err == nil {
			reader, err := GetTelegramFileReader(ctx, item, cfg)
			if err == nil {
				defer reader.Close()
				tmpPdf := thumbPath + "_temp.pdf"
				outFile, errCreate := os.Create(tmpPdf)
				if errCreate == nil {
					_, _ = io.Copy(outFile, io.LimitReader(reader, 10*1024*1024))
					outFile.Close()
					prefix := thumbPath + "_tmp"
					cmd := exec.CommandContext(ctx, pdftoppmPath, "-jpeg", "-f", "1", "-l", "1", "-scale-to", "320", "-singlefile", tmpPdf, prefix)
					cmd.Env = os.Environ()
					if err := cmd.Run(); err == nil {
						generated := prefix + ".jpg"
						if _, err := os.Stat(generated); err == nil {
							_ = os.Rename(generated, thumbPath)
							_ = os.Remove(tmpPdf)
							return successHandler(thumbPath)
						}
					}
					_ = os.Remove(tmpPdf)
				}
			}
		}
	}

	// 5. Handle Image types
	if strings.HasPrefix(actualMime, "image/") {
		success := false
		if reader, err := GetTelegramFileReader(ctx, item, cfg); err == nil {
			func() {
				defer reader.Close()
				if img, _, errDec := image.Decode(reader); errDec == nil {
					bounds := img.Bounds()
					width := bounds.Max.X
					height := bounds.Max.Y

					if width > 320 {
						height = (height * 320) / width
						width = 320
					}

					dst := image.NewRGBA(image.Rect(0, 0, width, height))
					draw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)

					out, errOut := os.Create(thumbPath)
					if errOut == nil {
						if errEnc := jpeg.Encode(out, dst, &jpeg.Options{Quality: 85}); errEnc == nil {
							success = true
						}
						out.Close()
					}
				} else {
					log.Printf("[Thumbnail] Go image decode failed for %s: %v. Trying FFmpeg fallback...", item.Filename, errDec)
				}
			}()
		}

		if success {
			return successHandler(thumbPath)
		}

		// Fallback to FFmpeg if Go decoder failed
		log.Printf("[Thumbnail] Go decoder failed or could not process. Running FFmpeg fallback for image: %s", item.Filename)
		if errFF := generateThumbnailWithFFmpeg(ctx, fileID, actualMime, &item, cfg, thumbPath, true); errFF == nil {
			return successHandler(thumbPath)
		} else {
			return nil, fmt.Errorf("failed to decode image with Go and FFmpeg: %w", errFF)
		}
	}

	// 6. Handle Video / Audio using local HTTP stream with FFmpeg
	if strings.HasPrefix(actualMime, "video/") || strings.HasPrefix(actualMime, "audio/") {
		if errFF := generateThumbnailWithFFmpeg(ctx, fileID, actualMime, &item, cfg, thumbPath, false); errFF != nil {
			return nil, errFF
		}
		return successHandler(thumbPath)
	}

	return nil, fmt.Errorf("unsupported file type for thumbnail generation")
}

func generateThumbnailWithFFmpeg(ctx context.Context, fileID int64, actualMime string, item *database.File, cfg *config.Config, thumbPath string, isImage bool) error {
	if cfg.FFMPEGPath == "disabled" || cfg.FFMPEGPath == "disable" || cfg.FFMPEGPath == "" {
		return fmt.Errorf("ffmpeg is disabled")
	}

	// Limit concurrent FFmpeg executions to prevent CPU starvation
	select {
	case ffmpegSemaphore <- struct{}{}:
		defer func() { <-ffmpegSemaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// Generate a temporary streaming token
	token := strings.ReplaceAll(uuid.New().String(), "-", "")
	TempStreamTokens.Store(token, TempStreamInfo{
		FileID:   int64(fileID),
		Username: item.Owner,
	})
	defer TempStreamTokens.Delete(token)

	// Construct local stream URL
	host := "127.0.0.1"
	if cfg.ListenAddr != "" && cfg.ListenAddr != "0.0.0.0" && cfg.ListenAddr != "::" {
		host = cfg.ListenAddr
	}
	localURL := fmt.Sprintf("http://%s:%s/api/temp-stream/%s", host, cfg.Port, token)

	var cmd *exec.Cmd
	if isImage {
		cmd = exec.Command(
			cfg.FFMPEGPath, "-y", "-i", localURL,
			"-vframes", "1",
			"-vf", "scale=320:-1", thumbPath,
		)
	} else if strings.HasPrefix(actualMime, "video/") {
		cmd = exec.Command(
			cfg.FFMPEGPath, "-y", "-ss", "00:00:01.000", "-i", localURL,
			"-vframes", "1",
			"-vf", "scale=320:-1", thumbPath,
		)
	} else { // audio/
		cmd = exec.Command(
			cfg.FFMPEGPath, "-y", "-i", localURL,
			"-an", "-vframes", "1",
			"-vf", "scale=320:-1", thumbPath,
		)
	}

	cmd.Env = os.Environ()

	// Run FFmpeg with a 30 second timeout to prevent hanging
	runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
	defer runCancel()

	cmd.Process = nil

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Run()
	}()

	select {
	case <-runCtx.Done():
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return fmt.Errorf("ffmpeg timed out: %w", runCtx.Err())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("ffmpeg error: %w", err)
		}
	}

	return nil
}

func extractZipCoverFromReader(r io.ReaderAt, size int64, thumbPath string) *string {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil
	}

	var targetFile *zip.File
	var coverCandidates []*zip.File
	var fallbackImages []*zip.File

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(f.Name)
		baseName := filepath.Base(name)
		if strings.HasPrefix(baseName, ".") || strings.Contains(name, "__macosx") {
			continue
		}

		isImg := strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") ||
			strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".webp") ||
			strings.HasSuffix(name, ".gif") || strings.HasSuffix(name, ".bmp")

		if isImg {
			if strings.Contains(baseName, "cover") || strings.Contains(baseName, "thumbnail") || strings.Contains(baseName, "bia") {
				coverCandidates = append(coverCandidates, f)
			}
			fallbackImages = append(fallbackImages, f)
		}
	}

	if len(coverCandidates) > 0 {
		sortZipFiles(coverCandidates)
		targetFile = coverCandidates[0]
	} else if len(fallbackImages) > 0 {
		sortZipFiles(fallbackImages)
		targetFile = fallbackImages[0]
	}

	if targetFile == nil {
		return nil
	}

	rc, err := targetFile.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}

	bounds := img.Bounds()
	width := bounds.Max.X
	height := bounds.Max.Y

	if width > 320 {
		height = (height * 320) / width
		width = 320
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)

	out, err := os.Create(thumbPath)
	if err != nil {
		return nil
	}
	defer out.Close()

	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil
	}

	return &thumbPath
}

func sortZipFiles(files []*zip.File) {
	sort.Slice(files, func(i, j int) bool {
		nameI := strings.ToLower(filepath.Base(files[i].Name))
		nameJ := strings.ToLower(filepath.Base(files[j].Name))
		exactI := strings.HasPrefix(nameI, "cover.")
		exactJ := strings.HasPrefix(nameJ, "cover.")
		if exactI && !exactJ {
			return true
		}
		if !exactI && exactJ {
			return false
		}
		return len(files[i].Name) < len(files[j].Name)
	})
}

var (
	thumbQueueOnce sync.Once
	thumbQueueChan chan int64
	thumbPending   sync.Map
)

func initThumbQueue(cfg *config.Config) {
	thumbQueueChan = make(chan int64, 1000)
	for i := 0; i < 2; i++ {
		go func() {
			for fid := range thumbQueueChan {
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[Thumbnail Worker] Recovered from panic for file ID %d: %v", fid, r)
						}
						thumbPending.Delete(fid)
					}()
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					_, _ = RegenerateFileThumbnail(ctx, fid, cfg)
				}()
			}
		}()
	}
}

func QueueThumbnailGeneration(fileID int64, cfg *config.Config) {
	if cfg == nil {
		return
	}
	thumbQueueOnce.Do(func() {
		initThumbQueue(cfg)
	})
	if _, loaded := thumbPending.LoadOrStore(fileID, true); !loaded {
		select {
		case thumbQueueChan <- fileID:
		default:
			thumbPending.Delete(fileID)
		}
	}
}

