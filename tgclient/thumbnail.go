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
	"github.com/gotd/td/tg"
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

var (
	thumbInflightMu sync.Mutex
	thumbInflight   = make(map[int64]chan struct{})
	thumbGenSem     = make(chan struct{}, 3)
)

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
	// Deduplicate in-flight thumbnail generations for the same file ID
	thumbInflightMu.Lock()
	if done, exists := thumbInflight[fileID]; exists {
		thumbInflightMu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		var current database.File
		if err := database.RODB.Get(&current, "SELECT thumb_path FROM files WHERE id = ?", fileID); err == nil && current.ThumbPath != nil {
			if _, errStat := os.Stat(*current.ThumbPath); errStat == nil {
				return current.ThumbPath, nil
			}
		}
		return nil, fmt.Errorf("concurrent thumbnail generation finished without file")
	}
	done := make(chan struct{})
	thumbInflight[fileID] = done
	thumbInflightMu.Unlock()

	defer func() {
		thumbInflightMu.Lock()
		delete(thumbInflight, fileID)
		close(done)
		thumbInflightMu.Unlock()
	}()

	// Limit concurrent generation to prevent CPU/memory exhaustion and Telegram flood waits
	select {
	case thumbGenSem <- struct{}{}:
		defer func() { <-thumbGenSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Use an independent context so generation finishes and persists even if client pauses/cancels request
	genCtx, genCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer genCancel()

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

	// Double check if thumbnail already exists on disk
	if item.ThumbPath != nil && *item.ThumbPath != "" {
		if _, err := os.Stat(*item.ThumbPath); err == nil {
			return item.ThumbPath, nil
		}
	}

	// 2. Determine file extension and MIME type
	rawMime := ""
	if item.MimeType != nil {
		rawMime = *item.MimeType
	}
	actualMime := utils.DetectMime(item.Filename, rawMime)
	ext := strings.ToLower(filepath.Ext(item.Filename))

	// 3. Define the output thumbnail name and path
	targetDir := cfg.ThumbsDir
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		// If static/thumbs fails (e.g. read-only filesystem or permissions), fallback to data/thumbs
		fallbackDir := filepath.Join("data", "thumbs")
		if errFb := os.MkdirAll(fallbackDir, 0755); errFb == nil {
			targetDir = fallbackDir
		} else {
			targetDir = filepath.Join(os.TempDir(), "telecloud_thumbs")
			_ = os.MkdirAll(targetDir, 0755)
		}
	}
	thumbName := strings.ReplaceAll(uuid.New().String(), "-", "") + ".jpg"
	thumbPath := filepath.Join(targetDir, thumbName)
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

	// 4. Try downloading native Telegram thumbnail (fastest: ~50ms, works for videos/photos/documents)
	firstMsgID := 0
	if item.MessageID != nil {
		firstMsgID = *item.MessageID
	} else if hasParts {
		var partMsgID int
		if errPart := database.RODB.Get(&partMsgID, "SELECT message_id FROM file_parts WHERE file_id = ? ORDER BY part_index ASC LIMIT 1", fileID); errPart == nil {
			firstMsgID = partMsgID
		}
	}

	if firstMsgID > 0 {
		api := GetAPI()
		ok, errNative := downloadTelegramThumbnail(genCtx, api, firstMsgID, cfg, thumbPath)
		if (!ok || errNative != nil) && api != Client.API() {
			ok, errNative = downloadTelegramThumbnail(genCtx, Client.API(), firstMsgID, cfg, thumbPath)
		}
		if ok {
			return successHandler(thumbPath)
		}
	}

	// 5. Handle EPUB / CBZ
	if ext == ".epub" || ext == ".cbz" || actualMime == "application/epub+zip" || actualMime == "application/x-cbz" {
		reader, err := GetTelegramFileReader(genCtx, item, cfg)
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

	// 6. Handle PDF
	if ext == ".pdf" || actualMime == "application/pdf" {
		if pdftoppmPath, err := exec.LookPath("pdftoppm"); err == nil {
			reader, err := GetTelegramFileReader(genCtx, item, cfg)
			if err == nil {
				defer reader.Close()
				tmpPdf := thumbPath + "_temp.pdf"
				outFile, errCreate := os.Create(tmpPdf)
				if errCreate == nil {
					_, _ = io.Copy(outFile, io.LimitReader(reader, 10*1024*1024))
					outFile.Close()
					prefix := thumbPath + "_tmp"
					cmd := exec.CommandContext(genCtx, pdftoppmPath, "-jpeg", "-f", "1", "-l", "1", "-scale-to", "320", "-singlefile", tmpPdf, prefix)
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
		if errFF := generateThumbnailWithFFmpeg(genCtx, fileID, actualMime, &item, cfg, thumbPath, true); errFF == nil {
			return successHandler(thumbPath)
		}
	}

	// 7. Handle Image types
	if strings.HasPrefix(actualMime, "image/") {
		success := false
		if reader, err := GetTelegramFileReader(genCtx, item, cfg); err == nil {
			func() {
				defer reader.Close()
				// Read into memory buffer with safe limit (15MB) to avoid fragmented streaming decode
				imgBytes, errRead := io.ReadAll(io.LimitReader(reader, 15*1024*1024))
				if errRead != nil || len(imgBytes) == 0 {
					log.Printf("[Thumbnail] Failed to read image bytes for %s: %v", item.Filename, errRead)
					return
				}

				if img, _, errDec := image.Decode(bytes.NewReader(imgBytes)); errDec == nil {
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
		} else {
			log.Printf("[Thumbnail] Failed to get reader for image %s: %v", item.Filename, err)
		}

		if success {
			return successHandler(thumbPath)
		}

		// Fallback to FFmpeg if Go decoder failed
		log.Printf("[Thumbnail] Running FFmpeg fallback for image: %s", item.Filename)
		if errFF := generateThumbnailWithFFmpeg(genCtx, fileID, actualMime, &item, cfg, thumbPath, true); errFF == nil {
			return successHandler(thumbPath)
		} else {
			return nil, fmt.Errorf("failed to decode image with Go and FFmpeg: %w", errFF)
		}
	}

	// 8. Handle Video / Audio using local HTTP stream with FFmpeg
	if strings.HasPrefix(actualMime, "video/") || strings.HasPrefix(actualMime, "audio/") {
		if errFF := generateThumbnailWithFFmpeg(genCtx, fileID, actualMime, &item, cfg, thumbPath, false); errFF != nil {
			return nil, errFF
		}
		return successHandler(thumbPath)
	}

	return nil, fmt.Errorf("unsupported file type for thumbnail generation")
}

func downloadTelegramThumbnail(ctx context.Context, api *tg.Client, msgID int, cfg *config.Config, thumbPath string) (bool, error) {
	peer, err := resolveLogGroup(ctx, api, cfg.LogGroupID)
	if err != nil {
		return false, err
	}

	msg, err := FetchTelegramMessage(ctx, api, peer, msgID)
	if err != nil {
		return false, err
	}

	if msg == nil || msg.Media == nil {
		return false, fmt.Errorf("message has no media")
	}

	var thumbLoc tg.InputFileLocationClass
	var inlineBytes []byte

	switch m := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok || len(doc.Thumbs) == 0 {
			return false, nil
		}
		var bestSize tg.PhotoSizeClass
		for _, sz := range doc.Thumbs {
			switch s := sz.(type) {
			case *tg.PhotoCachedSize:
				inlineBytes = s.Bytes
				break
			case *tg.PhotoSize, *tg.PhotoSizeProgressive:
				bestSize = sz
			}
		}
		if len(inlineBytes) == 0 && bestSize != nil {
			thumbLoc = &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
				ThumbSize:     bestSize.GetType(),
			}
		}

	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok || len(photo.Sizes) == 0 {
			return false, nil
		}
		var bestSize tg.PhotoSizeClass
		for _, sz := range photo.Sizes {
			switch s := sz.(type) {
			case *tg.PhotoCachedSize:
				inlineBytes = s.Bytes
				break
			case *tg.PhotoSize, *tg.PhotoSizeProgressive:
				t := sz.GetType()
				if t == "m" || t == "s" || bestSize == nil {
					bestSize = sz
				}
			}
		}
		if len(inlineBytes) == 0 && bestSize != nil {
			thumbLoc = &tg.InputPhotoFileLocation{
				ID:            photo.ID,
				AccessHash:    photo.AccessHash,
				FileReference: photo.FileReference,
				ThumbSize:     bestSize.GetType(),
			}
		}
	}

	if len(inlineBytes) > 0 {
		if err := os.WriteFile(thumbPath, inlineBytes, 0644); err == nil {
			return true, nil
		}
	}

	if thumbLoc != nil {
		req := &tg.UploadGetFileRequest{
			Precise:  true,
			Location: thumbLoc,
			Offset:   0,
			Limit:    1048576,
		}
		res, err := api.UploadGetFile(ctx, req)
		if err == nil {
			if upFile, ok := res.(*tg.UploadFile); ok && len(upFile.Bytes) > 0 {
				if err := os.WriteFile(thumbPath, upFile.Bytes, 0644); err == nil {
					return true, nil
				}
			}
		} else {
			log.Printf("[Thumbnail] Native thumbnail fetch failed for msgID %d: %v", msgID, err)
		}
	}

	return false, nil
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

	// Run FFmpeg with a 30 second timeout to prevent hanging
	runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
	defer runCancel()

	var cmd *exec.Cmd
	if isImage {
		cmd = exec.CommandContext(
			runCtx,
			cfg.FFMPEGPath, "-y", "-i", localURL,
			"-frames:v", "1", "-update", "1",
			"-vf", "scale=320:-1", thumbPath,
		)
	} else if strings.HasPrefix(actualMime, "video/") {
		cmd = exec.CommandContext(
			runCtx,
			cfg.FFMPEGPath, "-y", "-ss", "00:00:01.000", "-i", localURL,
			"-frames:v", "1", "-update", "1",
			"-vf", "scale=320:-1", thumbPath,
		)
	} else { // audio/
		cmd = exec.CommandContext(
			runCtx,
			cfg.FFMPEGPath, "-y", "-i", localURL,
			"-an", "-frames:v", "1", "-update", "1",
			"-vf", "scale=320:-1", thumbPath,
		)
	}

	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

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
			return fmt.Errorf("ffmpeg error (%w): %s", err, strings.TrimSpace(stderr.String()))
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

