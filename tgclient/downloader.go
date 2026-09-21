package tgclient

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"telecloud/config"
	"telecloud/database"

	"github.com/gotd/td/tg"
)

// BlockCache implements a thread-safe LRU cache for file chunks.
type BlockCache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	cache    map[string]*list.Element
}

type cacheEntry struct {
	key   string
	value []byte
}

func NewBlockCache(capacity int) *BlockCache {
	return &BlockCache{
		capacity: capacity,
		ll:       list.New(),
		cache:    make(map[string]*list.Element),
	}
}

func (c *BlockCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, hit := c.cache[key]; hit {
		c.ll.MoveToFront(ele)
		return ele.Value.(*cacheEntry).value, true
	}
	return nil, false
}

func (c *BlockCache) Add(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ele, hit := c.cache[key]; hit {
		c.ll.MoveToFront(ele)
		ele.Value.(*cacheEntry).value = value
		return
	}
	ele := c.ll.PushFront(&cacheEntry{key, value})
	c.cache[key] = ele
	if c.ll.Len() > c.capacity {
		c.removeOldest()
	}
}

func (c *BlockCache) removeOldest() {
	ele := c.ll.Back()
	if ele != nil {
		c.ll.Remove(ele)
		kv := ele.Value.(*cacheEntry)
		delete(c.cache, kv.key)
	}
}

var (
	locationCache    = make(map[int]*cachedLocation)
	cacheMutex       sync.RWMutex
	globalChunkCache = NewBlockCache(128) // 128 MB maximum buffer size

	// globalPrefetchSem caps read-ahead requests across *all* concurrent streams.
	// Without it every viewer independently keeps prefetchDepth chunks in flight,
	// so a few dozen viewers would swamp the handful of Telegram sessions and
	// trigger FLOOD_WAIT. Prefetch is best-effort: once the budget is exhausted
	// the reader silently falls back to fetching on demand, which is exactly the
	// behaviour before read-ahead existed.
	globalPrefetchSem = make(chan struct{}, 32)
)

func init() {
	// Dọn dẹp location cache expired mỗi 30 phút
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		for range ticker.C {
			now := time.Now()
			cacheMutex.Lock()
			for k, v := range locationCache {
				if now.After(v.expiresAt) {
					delete(locationCache, k)
				}
			}
			cacheMutex.Unlock()
		}
	}()
}

type cachedLocation struct {
	loc       tg.InputFileLocationClass
	api       *tg.Client // Store the API client that resolved this location
	expiresAt time.Time
}

type tgFileReader struct {
	ctx    context.Context
	cancel context.CancelFunc
	api    *tg.Client
	loc    tg.InputFileLocationClass
	size   int64
	offset int64
	msgID  int
	cfg    *config.Config

	// Current chunk (served synchronously)
	chunkData   []byte
	chunkOffset int64

	// Prefetch buffer
	prefetchChunks   map[int64][]byte        // offset → prefetched chunk data
	prefetchInflight map[int64]chan struct{} // offset → channel closed when that fetch finishes
	prefetchGen      uint64                  // bumped on Seek to discard in-flight results
	prefetchDepth    int                     // how many chunks to keep in flight ahead of the cursor
	prefetchMu       sync.Mutex
	prefetchSem      chan struct{} // caps concurrent prefetch goroutines for this reader
	locMu            sync.Mutex    // Protects loc and api updates during file reference refreshes

	// fetch performs the actual chunk download. Indirected so tests can run the
	// caching/prefetch logic without touching the network.
	fetch func(api *tg.Client, offset int64, limit int64) ([]byte, error)
}

// pickDownloadAPI selects the session used for background prefetches.
var pickDownloadAPI = GetAPI

func (r *tgFileReader) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

func (r *tgFileReader) Read(p []byte) (int, error) {
	if r.offset >= r.size {
		return 0, io.EOF
	}

	const chunkSize = int64(1024 * 1024)

	// If we have no data or the current offset is outside our cached chunk, load it
	if r.chunkData == nil || r.offset < r.chunkOffset || r.offset >= r.chunkOffset+int64(len(r.chunkData)) {
		chunkStart := (r.offset / chunkSize) * chunkSize

		// Kick off the read-ahead window BEFORE blocking on the current chunk, so the
		// following chunks are already downloading in parallel while we wait for this one.
		r.fillPrefetchWindow(chunkStart, chunkSize)

		data, err := r.getChunk(chunkStart, chunkSize)
		if err != nil {
			return 0, err
		}
		if r.offset >= chunkStart+int64(len(data)) {
			return 0, io.ErrUnexpectedEOF
		}
		r.chunkData = data
		r.chunkOffset = chunkStart
	}

	// Copy data to caller's buffer
	inChunkOffset := r.offset - r.chunkOffset
	n := copy(p, r.chunkData[inChunkOffset:])
	r.offset += int64(n)

	// Top the window back up as prefetch goroutines complete.
	r.fillPrefetchWindow(r.chunkOffset, chunkSize)

	return n, nil
}

// getChunk returns the chunk starting at chunkStart, preferring (in order) the
// prefetch buffer, an already in-flight prefetch, the global cache, and finally a
// synchronous fetch. Waiting on an in-flight prefetch avoids issuing a duplicate
// upload.getFile for a chunk that is already being downloaded.
func (r *tgFileReader) getChunk(chunkStart int64, chunkSize int64) ([]byte, error) {
	cacheKey := fmt.Sprintf("%d_%d", r.msgID, chunkStart)

	r.prefetchMu.Lock()
	if data, ok := r.prefetchChunks[chunkStart]; ok {
		delete(r.prefetchChunks, chunkStart)
		r.prefetchMu.Unlock()
		return data, nil
	}
	done, inflight := r.prefetchInflight[chunkStart]
	r.prefetchMu.Unlock()

	if inflight {
		select {
		case <-done:
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
		r.prefetchMu.Lock()
		if data, ok := r.prefetchChunks[chunkStart]; ok {
			delete(r.prefetchChunks, chunkStart)
			r.prefetchMu.Unlock()
			return data, nil
		}
		r.prefetchMu.Unlock()
		// Prefetch failed or was discarded by a seek; fall through.
	}

	if data, ok := globalChunkCache.Get(cacheKey); ok {
		return data, nil
	}

	data, err := r.fetch(r.api, chunkStart, chunkSize)
	if err != nil {
		return nil, err
	}
	globalChunkCache.Add(cacheKey, data)
	return data, nil
}

// fillPrefetchWindow keeps up to prefetchDepth chunks after fromChunk in flight.
func (r *tgFileReader) fillPrefetchWindow(fromChunk int64, chunkSize int64) {
	for i := int64(1); i <= int64(r.prefetchDepth); i++ {
		r.triggerPrefetch(fromChunk+i*chunkSize, chunkSize)
	}
}

// triggerPrefetch launches a background goroutine to fetch one chunk using a
// different bot than the synchronous path. This spreads the Telegram rate-limit
// across sessions without overwhelming a single DC.
func (r *tgFileReader) triggerPrefetch(offset int64, limit int64) {
	if offset >= r.size {
		return
	}

	// Check if already in global chunk cache
	cacheKey := fmt.Sprintf("%d_%d", r.msgID, offset)
	if _, ok := globalChunkCache.Get(cacheKey); ok {
		return
	}

	r.prefetchMu.Lock()
	// Already prefetched or being prefetched locally
	if _, exists := r.prefetchChunks[offset]; exists {
		r.prefetchMu.Unlock()
		return
	}
	if _, running := r.prefetchInflight[offset]; running {
		r.prefetchMu.Unlock()
		return
	}
	// Non-blocking: respect the per-reader concurrency cap.
	select {
	case r.prefetchSem <- struct{}{}:
	default:
		r.prefetchMu.Unlock()
		return
	}
	// Non-blocking: respect the server-wide read-ahead budget shared by all streams.
	select {
	case globalPrefetchSem <- struct{}{}:
	default:
		<-r.prefetchSem
		r.prefetchMu.Unlock()
		return
	}
	done := make(chan struct{})
	r.prefetchInflight[offset] = done
	gen := r.prefetchGen
	r.prefetchMu.Unlock()

	go func() {
		defer func() {
			r.prefetchMu.Lock()
			if r.prefetchInflight[offset] == done {
				delete(r.prefetchInflight, offset)
			}
			r.prefetchMu.Unlock()
			close(done)
			<-globalPrefetchSem
			<-r.prefetchSem
		}()

		// Use the same client session that owns r.loc to avoid FILEREF_INVALID / session mismatch
		data, err := r.fetch(r.api, offset, limit)
		if err != nil {
			return
		}

		// Store in global chunk cache
		globalChunkCache.Add(cacheKey, data)

		r.prefetchMu.Lock()
		// Discard if the caller seeked away: the chunk stays in the global cache,
		// but the per-reader buffer must not grow with chunks nobody will read.
		if r.prefetchGen == gen {
			r.prefetchChunks[offset] = data
		}
		r.prefetchMu.Unlock()
	}()
}

func (r *tgFileReader) fetchChunk(api *tg.Client, offset int64, limit int64) ([]byte, error) {
	r.locMu.Lock()
	loc := r.loc
	r.locMu.Unlock()

	req := &tg.UploadGetFileRequest{
		Precise:  true,
		Location: loc,
		Offset:   offset,
		Limit:    int(limit),
	}

	var res tg.UploadFileClass
	var err error
	refreshed := false
	for attempt := 0; attempt < 3; attempt++ {
		res, err = api.UploadGetFile(r.ctx, req)
		if err == nil {
			// Check for incomplete reads (less bytes than requested limit and not EOF)
			if fileObj, ok := res.(*tg.UploadFile); ok {
				if len(fileObj.Bytes) < int(limit) && offset+int64(len(fileObj.Bytes)) < r.size {
					err = fmt.Errorf("incomplete read from telegram: got %d bytes, expected %d", len(fileObj.Bytes), limit)
					// Sleep briefly and retry
					select {
					case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
						continue
					case <-r.ctx.Done():
						return nil, r.ctx.Err()
					}
				}
			}
			break
		}
		if r.ctx.Err() != nil {
			return nil, r.ctx.Err()
		}
		errStr := err.Error()

		if strings.Contains(errStr, "FILE_REFERENCE_EXPIRED") || strings.Contains(errStr, "FILEREF_INVALID") {
			if refreshed {
				// Already refreshed once in this fetchChunk call, don't loop infinitely
				continue
			}

			r.locMu.Lock()
			// If another thread (e.g. prefetch or main read) has already refreshed the location,
			// req.Location would be different from the updated r.loc.
			// In that case, we can just use the new r.loc and retry immediately.
			if r.loc != req.Location {
				req.Location = r.loc
				r.locMu.Unlock()
				refreshed = true
				attempt-- // Do not consume a retry attempt
				log.Printf("[downloader] Another thread refreshed location for msgID %d, retrying with new location...", r.msgID)
				continue
			}
			r.locMu.Unlock()

			log.Printf("[downloader] File reference expired/invalid for msgID %d, re-resolving...", r.msgID)

			// Invalidate local and global cache
			cacheMutex.Lock()
			delete(locationCache, r.msgID)
			cacheMutex.Unlock()

			// Re-resolve
			resolvedApi := api
			newLoc, errResolve := resolveMediaLocation(r.ctx, api, r.msgID, r.cfg)
			if errResolve != nil && api != Client.API() {
				newLoc, errResolve = resolveMediaLocation(r.ctx, Client.API(), r.msgID, r.cfg)
				if errResolve == nil {
					resolvedApi = Client.API()
				}
			}

			if errResolve == nil && newLoc != nil {
				r.locMu.Lock()
				r.loc = newLoc
				r.locMu.Unlock()

				cacheMutex.Lock()
				locationCache[r.msgID] = &cachedLocation{
					loc:       newLoc,
					api:       resolvedApi,
					expiresAt: time.Now().Add(1 * time.Hour),
				}
				cacheMutex.Unlock()

				api = resolvedApi
				r.api = resolvedApi
				req.Location = newLoc
				refreshed = true
				attempt-- // Do not consume a retry attempt
				log.Printf("[downloader] Successfully re-resolved location for msgID %d, retrying chunk download...", r.msgID)
				continue
			} else {
				log.Printf("[downloader] Failed to re-resolve location for msgID %d: %v", r.msgID, errResolve)
			}
		}

		if strings.Contains(errStr, "FLOOD_WAIT") || strings.Contains(errStr, "TIMEOUT") || strings.Contains(errStr, "RPC_CALL_FAIL") {
			waitDuration := time.Duration(attempt+1) * 2 * time.Second
			if secs, ok := ParseFloodWait(err); ok {
				// Take this session out of the rotation so other downloads stop
				// picking it while it is rate-limited.
				MarkBotCooldown(api, secs)
				waitDuration = time.Duration(secs) * time.Second

				// Waiting out a long flood would stall the stream. Retry immediately on
				// another session instead; GetAPI skips the ones in cooldown.
				if next := GetAPI(); next != api {
					api = next
					waitDuration = 0
				}
			}
			select {
			case <-time.After(waitDuration):
				continue
			case <-r.ctx.Done():
				return nil, r.ctx.Err()
			}
		}
		select {
		case <-time.After(time.Duration(attempt+1) * time.Second):
		case <-r.ctx.Done():
			return nil, r.ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}

	switch result := res.(type) {
	case *tg.UploadFile:
		if len(result.Bytes) == 0 && offset < r.size {
			return nil, fmt.Errorf("unexpected end of file from telegram at offset %d (expected %d)", offset, r.size)
		}
		return result.Bytes, nil
	case *tg.UploadFileCDNRedirect:
		return nil, fmt.Errorf("CDN redirect not supported")
	default:
		return nil, fmt.Errorf("unexpected type %T", res)
	}
}

func (r *tgFileReader) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = r.offset + offset
	case io.SeekEnd:
		newOffset = r.size + offset
	}
	if newOffset < 0 {
		newOffset = 0
	}
	if newOffset > r.size {
		newOffset = r.size
	}
	if newOffset != r.offset {
		r.offset = newOffset
		// Invalidate prefetch buffer and current chunk on seek. Bumping the generation
		// makes results of in-flight prefetches be dropped instead of piling up in the
		// buffer for a position nobody will read (they are still kept in the global cache).
		r.prefetchMu.Lock()
		r.prefetchChunks = make(map[int64][]byte)
		r.prefetchGen++
		r.prefetchMu.Unlock()
		r.chunkData = nil
		r.chunkOffset = 0
	}
	return r.offset, nil
}

func ServeTelegramFile(c *http.Request, w http.ResponseWriter, file database.File, cfg *config.Config) error {
	ctx := c.Context()

	reader, err := GetTelegramFileReader(ctx, file, cfg)
	if err != nil {
		return err
	}

	// Allow browser/player to seek and cache the stream
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Accel-Buffering", "no")

	// Set Content-Type if not already set
	if w.Header().Get("Content-Type") == "" && file.MimeType != nil {
		mime := *file.MimeType
		// Fallback for common types if stored as octet-stream
		lowerName := strings.ToLower(file.Filename)
		if mime == "application/octet-stream" {
			if strings.HasSuffix(lowerName, ".pdf") {
				mime = "application/pdf"
			} else if strings.HasSuffix(lowerName, ".epub") {
				mime = "application/epub+zip"
			}
		}
		// Special handling for MKV to ensure browser compatibility (especially Safari)
		if strings.HasSuffix(lowerName, ".mkv") {
			mime = "video/mp4"
		}
		w.Header().Set("Content-Type", mime)
	}

	// Set Content-Disposition only if not already set (e.g., by router for attachment)
	if w.Header().Get("Content-Disposition") == "" {
		// Use proper RFC 6266 encoding for filename to support non-ASCII characters and quotes
		// filename*=UTF-8''... is the standard for modern browsers
		encodedName := url.PathEscape(file.Filename)
		// We still provide the quoted filename for legacy browsers
		safeName := strings.ReplaceAll(file.Filename, `"`, `\"`)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"; filename*=UTF-8''%s`, safeName, encodedName))
	}

	defer reader.Close()
	http.ServeContent(w, c, file.Filename, file.CreatedAt, reader)
	return nil
}

func GetTelegramFileReader(ctx context.Context, file database.File, cfg *config.Config) (io.ReadSeekCloser, error) {
	// Check if this file has multiple parts
	parts, err := database.GetFileParts(file.ID)
	if err == nil && len(parts) > 1 {
		ctx, cancel := context.WithCancel(ctx)
		return &multiPartReader{
			ctx:    ctx,
			cancel: cancel,
			parts:  parts,
			size:   file.Size,
			cfg:    cfg,
		}, nil
	}

	// Single part (or legacy file)
	if file.MessageID == nil {
		return nil, fmt.Errorf("file has no message ID")
	}
	return getSinglePartReader(ctx, *file.MessageID, file.Size, cfg)
}

func resolveMediaLocation(ctx context.Context, targetApi *tg.Client, msgID int, cfg *config.Config) (tg.InputFileLocationClass, error) {
	peer, err := resolveLogGroup(ctx, targetApi, cfg.LogGroupID)
	if err != nil {
		return nil, err
	}

	var msgs tg.MessageClassArray
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		res, err := targetApi.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  channel.ChannelID,
				AccessHash: channel.AccessHash,
			},
			ID: []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
		})
		if err != nil {
			return nil, err
		}
		switch m := res.(type) {
		case *tg.MessagesMessages:
			msgs = m.Messages
		case *tg.MessagesMessagesSlice:
			msgs = m.Messages
		case *tg.MessagesChannelMessages:
			msgs = m.Messages
		}
	} else {
		res, err := targetApi.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}})
		if err != nil {
			return nil, err
		}
		switch m := res.(type) {
		case *tg.MessagesMessages:
			msgs = m.Messages
		case *tg.MessagesMessagesSlice:
			msgs = m.Messages
		case *tg.MessagesChannelMessages:
			msgs = m.Messages
		}
	}

	if len(msgs) == 0 {
		return nil, fmt.Errorf("message not found")
	}

	msg, ok := msgs[0].(*tg.Message)
	if !ok || msg.Media == nil {
		return nil, fmt.Errorf("message has no media")
	}

	if docMedia, ok := msg.Media.(*tg.MessageMediaDocument); ok {
		doc, ok := docMedia.Document.(*tg.Document)
		if !ok {
			return nil, fmt.Errorf("document is empty")
		}
		return doc.AsInputDocumentFileLocation(), nil
	}

	if photoMedia, ok := msg.Media.(*tg.MessageMediaPhoto); ok {
		photo, ok := photoMedia.Photo.(*tg.Photo)
		if !ok {
			return nil, fmt.Errorf("photo is empty")
		}
		// Find the best photo size available (largest w/h/size)
		var bestSizeClass tg.PhotoSizeClass
		var maxArea int
		for _, sz := range photo.Sizes {
			switch s := sz.(type) {
			case *tg.PhotoSize:
				area := s.W * s.H
				if area > maxArea {
					maxArea = area
					bestSizeClass = sz
				}
			case *tg.PhotoSizeProgressive:
				area := s.W * s.H
				if area > maxArea {
					maxArea = area
					bestSizeClass = sz
				}
			case *tg.PhotoCachedSize:
				area := s.W * s.H
				if area > maxArea {
					maxArea = area
					bestSizeClass = sz
				}
			}
		}

		if bestSizeClass == nil && len(photo.Sizes) > 0 {
			bestSizeClass = photo.Sizes[len(photo.Sizes)-1]
		}

		if bestSizeClass == nil {
			return nil, fmt.Errorf("no valid photo sizes found")
		}

		return &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     bestSizeClass.GetType(),
		}, nil
	}

	return nil, fmt.Errorf("media type not supported for download: %T", msg.Media)
}

var getSinglePartReader = func(ctx context.Context, msgID int, size int64, cfg *config.Config) (io.ReadSeekCloser, error) {
	ctx, cancel := context.WithCancel(ctx)

	// Check cache first
	cacheMutex.RLock()
	cached, ok := locationCache[msgID]
	cacheMutex.RUnlock()

	if ok && time.Now().Before(cached.expiresAt) {
		return newTGFileReader(ctx, cancel, cached.api, cached.loc, size, msgID, cfg), nil
	}

	// Helper function to resolve media from a specific API client
	resolve := func(targetApi *tg.Client) (tg.InputFileLocationClass, error) {
		return resolveMediaLocation(ctx, targetApi, msgID, cfg)
	}

	api := GetAPI()
	loc, err := resolve(api)

	// Fallback to main client if the selected bot failed to find the message/media or had any error
	if err != nil && api != Client.API() {
		mainApi := Client.API()
		if locRetry, errRetry := resolve(mainApi); errRetry == nil {
			api = mainApi
			loc = locRetry
			err = nil
		}
	}

	if err != nil {
		cancel()
		return nil, err
	}

	// Cache the location AND the API client for 1 hour
	cacheMutex.Lock()
	locationCache[msgID] = &cachedLocation{
		loc:       loc,
		api:       api,
		expiresAt: time.Now().Add(1 * time.Hour),
	}
	cacheMutex.Unlock()

	return newTGFileReader(ctx, cancel, api, loc, size, msgID, cfg), nil
}

func newTGFileReader(ctx context.Context, cancel context.CancelFunc, api *tg.Client, loc tg.InputFileLocationClass, size int64, msgID int, cfg *config.Config) *tgFileReader {
	depth := 4
	if cfg != nil && cfg.DownloadPrefetch > 0 {
		depth = cfg.DownloadPrefetch
	}
	r := &tgFileReader{
		ctx:              ctx,
		cancel:           cancel,
		api:              api,
		loc:              loc,
		size:             size,
		msgID:            msgID,
		cfg:              cfg,
		prefetchChunks:   make(map[int64][]byte),
		prefetchInflight: make(map[int64]chan struct{}),
		prefetchDepth:    depth,
		prefetchSem:      make(chan struct{}, depth),
	}
	r.fetch = r.fetchChunk
	return r
}

type multiPartReader struct {
	ctx    context.Context
	cancel context.CancelFunc
	parts  []database.FilePart
	size   int64
	offset int64
	cfg    *config.Config

	currentReader        io.ReadSeekCloser
	currentIndex         int
	currentPartRemaining int64 // bytes left in current part before EOF

	// Pre-initialized next part reader (avoids gap between parts)
	mu              sync.Mutex
	nextReader      io.ReadSeekCloser
	nextPartIndex   int
	nextInitialized bool
}

func (r *multiPartReader) Close() error {
	if r.currentReader != nil {
		r.currentReader.Close()
	}

	r.mu.Lock()
	if r.nextReader != nil {
		r.nextReader.Close()
		r.nextReader = nil
	}
	r.nextPartIndex = -1
	r.nextInitialized = true // Prevent spawning any new prefetching
	r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

func (r *multiPartReader) Read(p []byte) (int, error) {
	if r.offset >= r.size {
		return 0, io.EOF
	}

	for {
		if r.currentReader == nil {
			// Use pre-initialized nextReader if available and matches
			r.mu.Lock()
			nextReader := r.nextReader
			nextPartIdx := r.nextPartIndex
			r.nextReader = nil
			r.nextInitialized = false
			r.mu.Unlock()

			if nextReader != nil && nextPartIdx == r.currentIndex {
				r.currentReader = nextReader
				r.currentPartRemaining = r.parts[r.currentIndex].Size
			} else {
				if nextReader != nil {
					nextReader.Close()
				}

				// Find which part contains the current offset
				var partStart int64
				found := false
				for i, part := range r.parts {
					if r.offset < partStart+part.Size {
						r.currentIndex = i
						reader, err := getSinglePartReader(r.ctx, part.MessageID, part.Size, r.cfg)
						if err != nil {
							return 0, err
						}
						relOffset := r.offset - partStart
						if relOffset > 0 {
							if _, err = reader.Seek(relOffset, io.SeekStart); err != nil {
								return 0, err
							}
						}
						r.currentReader = reader
						r.currentPartRemaining = part.Size - relOffset
						found = true
						break
					}
					partStart += part.Size
				}
				if !found {
					return 0, io.EOF
				}
			}
		}

		n, err := r.currentReader.Read(p)
		if n > 0 {
			r.offset += int64(n)
			r.currentPartRemaining -= int64(n)

			// Pre-initialize next part reader when approaching end of current part.
			// This eliminates the gap between parts by resolving the next message
			// location and prefetching its first chunk while we still have data to serve.
			const prefetchThreshold = int64(2 * 1024 * 1024) // 2MB
			nextIdx := r.currentIndex + 1

			r.mu.Lock()
			shouldPrefetch := !r.nextInitialized && r.currentPartRemaining <= prefetchThreshold && nextIdx < len(r.parts)
			if shouldPrefetch {
				r.nextInitialized = true
				r.nextPartIndex = nextIdx
				nextPart := r.parts[nextIdx]
				r.mu.Unlock()

				go func() {
					reader, err := getSinglePartReader(r.ctx, nextPart.MessageID, nextPart.Size, r.cfg)

					r.mu.Lock()
					defer r.mu.Unlock()

					// Check if reader has been closed/cancelled in the meantime
					if r.ctx.Err() != nil {
						if err == nil && reader != nil {
							reader.Close()
						}
						return
					}

					// Check if the user has seeked away to a different part index
					if r.nextPartIndex != nextIdx {
						if err == nil && reader != nil {
							reader.Close()
						}
						return
					}

					if err == nil {
						r.nextReader = reader
					} else {
						// Allow retry on error
						r.nextInitialized = false
					}
				}()
			} else {
				r.mu.Unlock()
			}

			return n, nil
		}
		if err == io.EOF {
			r.currentReader.Close()
			r.currentReader = nil
			r.currentIndex++
			r.currentPartRemaining = 0
			if r.currentIndex >= len(r.parts) {
				return 0, io.EOF
			}
			continue
		}
		return n, err
	}
}

func (r *multiPartReader) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = r.offset + offset
	case io.SeekEnd:
		newOffset = r.size + offset
	}

	if newOffset < 0 {
		newOffset = 0
	}
	if newOffset > r.size {
		newOffset = r.size
	}

	if newOffset != r.offset {
		r.offset = newOffset
		if r.currentReader != nil {
			r.currentReader.Close()
			r.currentReader = nil
		}

		r.mu.Lock()
		if r.nextReader != nil {
			r.nextReader.Close()
			r.nextReader = nil
		}
		// Reset state so prefetching can trigger for the new part
		r.nextPartIndex = -1
		r.nextInitialized = false
		r.mu.Unlock()
	}
	return r.offset, nil
}
