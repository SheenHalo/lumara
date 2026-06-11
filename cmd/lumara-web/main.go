package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultBaseURL     = "https://sdo.gsfc.nasa.gov/assets/img/browse"
	defaultUserAgent   = "lumara-sdo-web/1.0"
	defaultThumbMaxDim = 280
	maxImageListLimit  = 1000
	imageRetention     = 12 * time.Hour
	cleanupInterval    = 10 * time.Minute
)

var (
	resolutionOptions = []string{"512", "1024", "2048", "4096"}
	channelOptions    = []channelOption{
		{ID: "0304", Name: "Chromosphere", Title: "色球层", Description: "常用来观察日珥、耀斑和太阳边缘活动"},
		{ID: "0171", Name: "Quiet Corona", Title: "宁静日冕", Description: "适合观察较安静的日冕环结构"},
		{ID: "0193", Name: "Corona", Title: "日冕", Description: "适合观察日冕洞、热等离子体和较大尺度结构"},
		{ID: "0211", Name: "Active Regions", Title: "活跃区", Description: "适合观察高温活跃区和磁场活动区域"},
		{ID: "0094", Name: "Hot Flares", Title: "高温耀斑", Description: "适合观察非常热的耀斑区域"},
		{ID: "0131", Name: "Flare Plasma", Title: "耀斑等离子体", Description: "适合观察强耀斑和极高温等离子体"},
		{ID: "0335", Name: "Active Corona", Title: "活跃日冕", Description: "适合观察活跃日冕区域和环状结构"},
		{ID: "1600", Name: "Transition", Title: "过渡区", Description: "观察光球层到日冕之间的过渡区域"},
		{ID: "1700", Name: "Surface", Title: "太阳表面", Description: "接近可见光表面，适合观察表面纹理"},
		{ID: "HMIIF", Name: "Sunspots", Title: "太阳黑子", Description: "白光强度图，适合观察太阳黑子"},
		{ID: "HMIBC", Name: "Magnetic Field", Title: "磁场图", Description: "显示太阳表面磁场的正负极分布"},
	}

	fileNamePattern = regexp.MustCompile(`^(\d{8})_(\d{6})_(\d+)_([^/]+)\.jpg$`)
	linePattern     = regexp.MustCompile(`<a href="([^"]+\.jpg)">[^<]*</a>\s+\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}\s+([0-9.]+)([KMG]?)`)
	hrefPattern     = regexp.MustCompile(`href="([^"]+\.jpg)"`)
)

type serverConfig struct {
	addr            string
	baseURL         string
	output          string
	staticDir       string
	imageRetention  time.Duration
	cleanupInterval time.Duration
}

type channelOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type apiError struct {
	Error string `json:"error"`
}

type app struct {
	cfg      serverConfig
	client   *http.Client
	jobs     *jobStore
	archives *archiveJobStore
}

type jobStore struct {
	mu   sync.RWMutex
	jobs map[string]*downloadJob
	path string
}

type downloadJob struct {
	mu        sync.RWMutex
	cancel    context.CancelFunc
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Request   jobInput  `json:"request"`
	Created   time.Time `json:"createdAt"`
	Started   time.Time `json:"startedAt,omitempty"`
	Finished  time.Time `json:"finishedAt,omitempty"`
	CleanedAt time.Time `json:"cleanedAt,omitempty"`
	Found     int64     `json:"found"`
	Bytes     int64     `json:"bytes"`
	Done      int64     `json:"downloaded"`
	Skipped   int64     `json:"skipped"`
	Failed    int64     `json:"failed"`
	Current   string    `json:"current"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	Logs      []string  `json:"logs"`
	Paths     []string  `json:"paths,omitempty"`
}

type jobInput struct {
	From           string   `json:"from"`
	To             string   `json:"to"`
	Resolutions    []string `json:"resolutions"`
	Channels       []string `json:"channels"`
	Limit          int      `json:"limit"`
	Workers        int      `json:"workers"`
	VerifyExisting bool     `json:"verifyExisting"`
}

type jobSnapshot struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Request   jobInput  `json:"request"`
	Created   time.Time `json:"createdAt"`
	Started   time.Time `json:"startedAt,omitempty"`
	Finished  time.Time `json:"finishedAt,omitempty"`
	CleanedAt time.Time `json:"cleanedAt,omitempty"`
	Found     int64     `json:"found"`
	Bytes     int64     `json:"bytes"`
	Done      int64     `json:"downloaded"`
	Skipped   int64     `json:"skipped"`
	Failed    int64     `json:"failed"`
	Current   string    `json:"current"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	Logs      []string  `json:"logs"`
}

type storedJob struct {
	jobSnapshot
	Paths []string `json:"paths,omitempty"`
}

type remoteFile struct {
	name string
	size int64
}

type downloadItem struct {
	day        time.Time
	resolution string
	channel    string
	url        string
	filename   string
	size       int64
}

type imageInfo struct {
	Path        string    `json:"path"`
	Filename    string    `json:"filename"`
	Date        string    `json:"date"`
	Time        string    `json:"time"`
	Resolution  string    `json:"resolution"`
	Channel     string    `json:"channel"`
	ChannelName string    `json:"channelName"`
	Size        int64     `json:"size"`
	ModifiedAt  time.Time `json:"modifiedAt"`
	Thumbnail   string    `json:"thumbnailUrl"`
	Original    string    `json:"originalUrl"`
}

type imageListResponse struct {
	Items  []imageInfo `json:"items"`
	Total  int         `json:"total"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

type archiveRequest struct {
	Paths       []string `json:"paths"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	Resolution  string   `json:"resolution"`
	Channel     string   `json:"channel"`
	Name        string   `json:"name"`
	CurrentOnly bool     `json:"currentOnly"`
}

type archiveJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*serverArchiveJob
}

type serverArchiveJob struct {
	mu          sync.RWMutex
	ID          string    `json:"id"`
	SourceJobID string    `json:"sourceJobId"`
	Status      string    `json:"status"`
	Name        string    `json:"name"`
	Created     time.Time `json:"createdAt"`
	Finished    time.Time `json:"finishedAt,omitempty"`
	Total       int       `json:"total"`
	Done        int       `json:"done"`
	Bytes       int64     `json:"bytes"`
	Message     string    `json:"message"`
	Error       string    `json:"error,omitempty"`
	DownloadURL string    `json:"downloadUrl,omitempty"`
	FilePath    string    `json:"-"`
}

type archiveJobSnapshot struct {
	ID          string    `json:"id"`
	SourceJobID string    `json:"sourceJobId"`
	Status      string    `json:"status"`
	Name        string    `json:"name"`
	Created     time.Time `json:"createdAt"`
	Finished    time.Time `json:"finishedAt,omitempty"`
	Total       int       `json:"total"`
	Done        int       `json:"done"`
	Bytes       int64     `json:"bytes"`
	Message     string    `json:"message"`
	Error       string    `json:"error,omitempty"`
	DownloadURL string    `json:"downloadUrl,omitempty"`
}

func main() {
	cfg := serverConfig{}
	flag.StringVar(&cfg.addr, "addr", ":8090", "HTTP listen address")
	flag.StringVar(&cfg.baseURL, "base-url", defaultBaseURL, "NASA SDO browse base URL")
	flag.StringVar(&cfg.output, "output", "downloads", "download output directory")
	flag.StringVar(&cfg.staticDir, "static-dir", "", "Vue build output directory")
	flag.DurationVar(&cfg.imageRetention, "image-retention", imageRetention, "how long to keep task images after the task finishes; <=0 disables cleanup")
	flag.DurationVar(&cfg.cleanupInterval, "cleanup-interval", cleanupInterval, "finished task image cleanup interval")
	flag.Parse()

	absOutput, err := filepath.Abs(cfg.output)
	if err != nil {
		log.Fatalf("resolve output dir: %v", err)
	}
	cfg.output = absOutput
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	if err := os.MkdirAll(cfg.output, 0755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application := &app{
		cfg:      cfg,
		client:   &http.Client{Timeout: 90 * time.Second},
		jobs:     newJobStore(filepath.Join(cfg.output, ".lumara-jobs.json")),
		archives: newArchiveJobStore(),
	}
	if err := application.jobs.load(); err != nil {
		log.Printf("load job history failed: %v", err)
	}
	if cfg.imageRetention > 0 && cfg.cleanupInterval > 0 {
		go application.runImageCleanup(ctx)
	}

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           application.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("lumara web listening on %s", cfg.addr)
	log.Printf("download output: %s", cfg.output)
	if cfg.staticDir != "" {
		log.Printf("serving frontend: %s", cfg.staticDir)
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server failed: %v", err)
	}
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/options", a.handleOptions)
	mux.HandleFunc("/api/downloads", a.handleDownloads)
	mux.HandleFunc("/api/downloads/", a.handleDownloadByID)
	mux.HandleFunc("/api/archive-jobs/", a.handleArchiveJobByID)
	mux.HandleFunc("/api/images", a.handleImages)
	mux.HandleFunc("/api/file", a.handleFile)
	mux.HandleFunc("/api/thumb", a.handleThumbnail)
	mux.HandleFunc("/api/archive", a.handleArchive)
	mux.HandleFunc("/api/health", a.handleHealth)

	if a.cfg.staticDir != "" {
		mux.Handle("/", spaHandler(a.cfg.staticDir))
	}

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func spaHandler(root string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(root))
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
			return
		}
		cleanPath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleanPath == "." || strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
			http.NotFound(w, r)
			return
		}
		target := filepath.Join(root, cleanPath)
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	}
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) handleOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resolutions": resolutionOptions,
		"channels":    channelOptions,
	})
}

func (a *app) handleDownloads(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": a.jobs.list()})
	case http.MethodPost:
		var input jobInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是有效 JSON")
			return
		}
		normalized, days, err := normalizeJobInput(input)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		job := newDownloadJob(normalized)
		ctx, cancel := context.WithCancel(context.Background())
		job.cancel = cancel
		a.jobs.add(job)
		a.persistJobs()
		go a.runDownloadJob(ctx, job, days)
		writeJSON(w, http.StatusAccepted, job.snapshot())
	default:
		methodNotAllowed(w)
	}
}

func (a *app) handleDownloadByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/downloads/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	job := a.jobs.get(parts[0])
	if job == nil {
		writeError(w, http.StatusNotFound, "下载任务不存在")
		return
	}

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		job.cancelJob()
		a.persistJobs()
		writeJSON(w, http.StatusOK, job.snapshot())
		return
	}

	if len(parts) == 2 && parts[1] == "images" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		items, total, limit, offset, err := a.queryImagesForJob(job, r.URL.Query())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, imageListResponse{
			Items:  items,
			Total:  total,
			Limit:  limit,
			Offset: offset,
		})
		return
	}

	if len(parts) == 2 && parts[1] == "archive" {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		if err := a.writeJobArchive(w, job); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	if len(parts) == 2 && parts[1] == "archive-jobs" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		a.handleCreateJobArchive(w, job)
		return
	}

	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, job.snapshot())
	case http.MethodDelete:
		if jobIsActive(job.snapshot().Status) {
			writeError(w, http.StatusConflict, "下载中的任务不能删除")
			return
		}
		a.jobs.delete(job.ID)
		a.persistJobs()
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		methodNotAllowed(w)
	}
}

func (a *app) handleImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items, total, limit, offset, err := a.queryImages(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, imageListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

func (a *app) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	relPath := r.URL.Query().Get("path")
	absPath, err := a.safeOutputPath(relPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.ServeFile(w, r, absPath)
}

func (a *app) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	relPath := r.URL.Query().Get("path")
	absPath, err := a.safeOutputPath(relPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	thumbPath, err := a.ensureThumbnail(relPath, absPath)
	if err != nil {
		log.Printf("thumbnail fallback for %s: %v", relPath, err)
		http.ServeFile(w, r, absPath)
		return
	}
	http.ServeFile(w, r, thumbPath)
}

func (a *app) handleArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是有效 JSON")
		return
	}

	paths := input.Paths
	if len(paths) == 0 {
		values := url.Values{}
		values.Set("limit", strconv.Itoa(maxImageListLimit))
		if input.From != "" {
			values.Set("from", input.From)
		}
		if input.To != "" {
			values.Set("to", input.To)
		}
		if input.Resolution != "" {
			values.Set("resolution", input.Resolution)
		}
		if input.Channel != "" {
			values.Set("channel", input.Channel)
		}
		items, err := a.collectImages(values)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		paths = make([]string, 0, len(items))
		for _, item := range items {
			paths = append(paths, item.Path)
		}
	}
	if len(paths) == 0 {
		writeError(w, http.StatusNotFound, "没有可打包的图片")
		return
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "lumara-images.zip"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}

	files := make([]struct {
		rel string
		abs string
	}, 0, len(paths))
	seen := make(map[string]bool)
	for _, rel := range paths {
		if seen[rel] {
			continue
		}
		abs, err := a.safeOutputPath(rel)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			seen[rel] = true
			files = append(files, struct {
				rel string
				abs string
			}{rel: rel, abs: abs})
		}
	}
	if len(files) == 0 {
		writeError(w, http.StatusNotFound, "没有可打包的图片")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, file := range files {
		if err := addFileToZip(zipWriter, file.rel, file.abs); err != nil {
			log.Printf("zip add failed: %v", err)
			return
		}
	}
}

func (a *app) handleCreateJobArchive(w http.ResponseWriter, job *downloadJob) {
	items, err := a.collectJobImages(job)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "这个任务还没有可打包的图片")
		return
	}

	name := buildJobArchiveName(job)
	archiveDir := filepath.Join(a.cfg.output, ".archives")
	archive := newServerArchiveJob(job.ID, name, filepath.Join(archiveDir, randomID()+".zip"), len(items))
	a.archives.add(archive)
	go a.runArchiveJob(archive, items)
	writeJSON(w, http.StatusAccepted, archive.snapshot())
}

func (a *app) handleArchiveJobByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/archive-jobs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	archive := a.archives.get(parts[0])
	if archive == nil {
		writeError(w, http.StatusNotFound, "压缩任务不存在或服务已重启")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, archive.snapshot())
		return
	}

	if len(parts) == 2 && parts[1] == "file" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		snapshot := archive.snapshot()
		if snapshot.Status != "done" {
			writeError(w, http.StatusConflict, "压缩包还没有准备好")
			return
		}
		if _, err := os.Stat(archive.FilePath); err != nil {
			writeError(w, http.StatusNotFound, "压缩包文件不存在")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", snapshot.Name))
		http.ServeFile(w, r, archive.FilePath)
		return
	}

	http.NotFound(w, r)
}

func (a *app) writeJobArchive(w http.ResponseWriter, job *downloadJob) error {
	items, err := a.collectJobImages(job)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New("这个任务还没有可下载的图片")
	}

	name := fmt.Sprintf(
		"lumara-%s_%s_%s.zip",
		job.Request.From,
		strings.Join(job.Request.Resolutions, "-"),
		strings.Join(job.Request.Channels, "-"),
	)
	name = strings.ReplaceAll(name, "/", "-")

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, item := range items {
		abs, err := a.safeOutputPath(item.Path)
		if err != nil {
			return err
		}
		if err := addFileToZip(zipWriter, item.Path, abs); err != nil {
			return err
		}
	}
	return nil
}

func newArchiveJobStore() *archiveJobStore {
	return &archiveJobStore{jobs: make(map[string]*serverArchiveJob)}
}

func (s *archiveJobStore) add(job *serverArchiveJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
}

func (s *archiveJobStore) get(id string) *serverArchiveJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

func newServerArchiveJob(sourceJobID, name, filePath string, total int) *serverArchiveJob {
	id := randomID()
	return &serverArchiveJob{
		ID:          id,
		SourceJobID: sourceJobID,
		Status:      "queued",
		Name:        name,
		Created:     time.Now(),
		Total:       total,
		Message:     "等待压缩",
		DownloadURL: "/api/archive-jobs/" + id + "/file",
		FilePath:    filePath,
	}
}

func (j *serverArchiveJob) snapshot() archiveJobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return archiveJobSnapshot{
		ID:          j.ID,
		SourceJobID: j.SourceJobID,
		Status:      j.Status,
		Name:        j.Name,
		Created:     j.Created,
		Finished:    j.Finished,
		Total:       j.Total,
		Done:        j.Done,
		Bytes:       j.Bytes,
		Message:     j.Message,
		Error:       j.Error,
		DownloadURL: j.DownloadURL,
	}
}

func (j *serverArchiveJob) setRunning() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "running"
	j.Message = "正在创建压缩包"
}

func (j *serverArchiveJob) setProgress(done int, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Done = done
	j.Message = message
}

func (j *serverArchiveJob) setDone(bytes int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "done"
	j.Done = j.Total
	j.Bytes = bytes
	j.Message = "压缩完成"
	j.Finished = time.Now()
}

func (j *serverArchiveJob) setFailed(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "failed"
	j.Error = err.Error()
	j.Message = err.Error()
	j.Finished = time.Now()
}

func (a *app) runArchiveJob(archive *serverArchiveJob, items []imageInfo) {
	archive.setRunning()
	if err := os.MkdirAll(filepath.Dir(archive.FilePath), 0755); err != nil {
		archive.setFailed(err)
		return
	}

	tmpPath := archive.FilePath + ".part"
	_ = os.Remove(tmpPath)
	output, err := os.Create(tmpPath)
	if err != nil {
		archive.setFailed(err)
		return
	}

	zipWriter := zip.NewWriter(output)
	failed := false
	for index, item := range items {
		abs, err := a.safeOutputPath(item.Path)
		if err != nil {
			archive.setFailed(err)
			failed = true
			break
		}
		if err := addFileToZip(zipWriter, item.Path, abs); err != nil {
			archive.setFailed(err)
			failed = true
			break
		}
		archive.setProgress(index+1, fmt.Sprintf("正在压缩 %d/%d", index+1, len(items)))
	}

	closeErr := zipWriter.Close()
	fileCloseErr := output.Close()
	if failed {
		_ = os.Remove(tmpPath)
		return
	}
	if closeErr != nil {
		archive.setFailed(closeErr)
		_ = os.Remove(tmpPath)
		return
	}
	if fileCloseErr != nil {
		archive.setFailed(fileCloseErr)
		_ = os.Remove(tmpPath)
		return
	}
	_ = os.Remove(archive.FilePath)
	if err := os.Rename(tmpPath, archive.FilePath); err != nil {
		archive.setFailed(err)
		_ = os.Remove(tmpPath)
		return
	}
	info, err := os.Stat(archive.FilePath)
	if err != nil {
		archive.setFailed(err)
		return
	}
	archive.setDone(info.Size())
}

func buildJobArchiveName(job *downloadJob) string {
	name := fmt.Sprintf(
		"lumara-%s_%s_%s.zip",
		job.Request.From,
		strings.Join(job.Request.Resolutions, "-"),
		strings.Join(job.Request.Channels, "-"),
	)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "")
	return name
}

func normalizeJobInput(input jobInput) (jobInput, []time.Time, error) {
	input.From = strings.TrimSpace(input.From)
	input.To = strings.TrimSpace(input.To)
	if input.From == "" {
		return input, nil, errors.New("请选择开始日期")
	}
	if input.To == "" {
		input.To = input.From
	}
	from, err := parseDay(input.From)
	if err != nil {
		return input, nil, err
	}
	to, err := parseDay(input.To)
	if err != nil {
		return input, nil, err
	}
	if to.Before(from) {
		return input, nil, errors.New("结束日期不能早于开始日期")
	}
	days := make([]time.Time, 0, int(to.Sub(from).Hours()/24)+1)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		days = append(days, day)
	}

	input.Resolutions = uniqueStrings(input.Resolutions)
	if len(input.Resolutions) == 0 {
		input.Resolutions = []string{"512"}
	}
	if err := validateOptions(input.Resolutions, resolutionOptions, "分辨率"); err != nil {
		return input, nil, err
	}

	allowedChannels := make([]string, 0, len(channelOptions))
	for _, option := range channelOptions {
		allowedChannels = append(allowedChannels, option.ID)
	}
	input.Channels = uniqueStrings(input.Channels)
	if len(input.Channels) == 0 {
		input.Channels = []string{"0304"}
	}
	if err := validateOptions(input.Channels, allowedChannels, "图片类型"); err != nil {
		return input, nil, err
	}

	if input.Workers <= 0 {
		input.Workers = 4
	}
	if input.Workers > 16 {
		input.Workers = 16
	}
	if input.Limit < 0 {
		input.Limit = 0
	}
	return input, days, nil
}

func newDownloadJob(input jobInput) *downloadJob {
	return &downloadJob{
		ID:      randomID(),
		Status:  "queued",
		Request: input,
		Created: time.Now(),
		Logs:    []string{},
		Paths:   []string{},
	}
}

func newJobStore(path string) *jobStore {
	return &jobStore{
		jobs: make(map[string]*downloadJob),
		path: path,
	}
}

func (s *jobStore) add(job *downloadJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
}

func (s *jobStore) get(id string) *downloadJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

func (s *jobStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
}

func (s *jobStore) list() []jobSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]jobSnapshot, 0, len(s.jobs))
	for _, job := range s.jobs {
		items = append(items, job.snapshot())
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.After(items[j].Created)
	})
	return items
}

func (s *jobStore) load() error {
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored []storedJob
	if err := json.Unmarshal(body, &stored); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range stored {
		job := &downloadJob{
			ID:        item.ID,
			Status:    item.Status,
			Request:   item.Request,
			Created:   item.Created,
			Started:   item.Started,
			Finished:  item.Finished,
			CleanedAt: item.CleanedAt,
			Found:     item.Found,
			Bytes:     item.Bytes,
			Done:      item.Done,
			Skipped:   item.Skipped,
			Failed:    item.Failed,
			Current:   item.Current,
			Message:   item.Message,
			Error:     item.Error,
			Logs:      item.Logs,
			Paths:     item.Paths,
		}
		if job.Status == "queued" || job.Status == "running" {
			job.Status = "canceled"
			job.Message = "服务重启，任务已停止"
			if job.Finished.IsZero() {
				job.Finished = time.Now()
			}
		}
		if job.Logs == nil {
			job.Logs = []string{}
		}
		if job.Paths == nil {
			job.Paths = []string{}
		}
		s.jobs[job.ID] = job
	}
	return nil
}

func (s *jobStore) save() error {
	s.mu.RLock()
	items := make([]storedJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		snapshot := job.snapshot()
		job.mu.RLock()
		paths := append([]string(nil), job.Paths...)
		job.mu.RUnlock()
		items = append(items, storedJob{
			jobSnapshot: snapshot,
			Paths:       paths,
		})
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.Before(items[j].Created)
	})
	body, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (a *app) persistJobs() {
	if err := a.jobs.save(); err != nil {
		log.Printf("save job history failed: %v", err)
	}
}

type cleanupCandidate struct {
	jobID string
	paths []string
}

type cleanupResult struct {
	deleted   int
	missing   int
	protected int
	failed    int
}

func (a *app) runImageCleanup(ctx context.Context) {
	a.cleanupExpiredJobImages(time.Now())

	ticker := time.NewTicker(a.cfg.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.cleanupExpiredJobImages(now)
		}
	}
}

func (a *app) cleanupExpiredJobImages(now time.Time) {
	candidates, protectedPaths := a.jobs.cleanupPlan(now, a.cfg.imageRetention)
	if len(candidates) == 0 {
		return
	}

	changed := false
	for _, candidate := range candidates {
		result := a.deleteJobImageFiles(candidate.paths, protectedPaths)
		job := a.jobs.get(candidate.jobID)
		if job == nil {
			continue
		}
		if result.failed > 0 {
			job.addLog(
				"自动清理图片部分失败：删除 %d，已不存在 %d，保留 %d，失败 %d",
				result.deleted,
				result.missing,
				result.protected,
				result.failed,
			)
			changed = true
			continue
		}
		job.markImagesCleaned(now, result)
		changed = true
	}
	if changed {
		a.persistJobs()
	}
}

func (s *jobStore) cleanupPlan(now time.Time, retention time.Duration) ([]cleanupCandidate, map[string]bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	candidateIDs := make(map[string]bool)
	candidates := make([]cleanupCandidate, 0)
	for _, job := range s.jobs {
		job.mu.RLock()
		due := jobCleanupDue(job.Status, job.Finished, job.CleanedAt, now, retention)
		paths := uniquePathList(job.Paths)
		id := job.ID
		job.mu.RUnlock()
		if !due {
			continue
		}
		candidateIDs[id] = true
		candidates = append(candidates, cleanupCandidate{jobID: id, paths: paths})
	}

	protectedPaths := make(map[string]bool)
	for _, job := range s.jobs {
		job.mu.RLock()
		protect := !candidateIDs[job.ID] && job.CleanedAt.IsZero()
		if protect {
			for _, path := range job.Paths {
				protectedPaths[path] = true
			}
		}
		job.mu.RUnlock()
	}
	return candidates, protectedPaths
}

func jobCleanupDue(status string, finished, cleanedAt, now time.Time, retention time.Duration) bool {
	if !jobIsTerminal(status) || !cleanedAt.IsZero() || finished.IsZero() {
		return false
	}
	return !now.Before(finished.Add(retention))
}

func (a *app) deleteJobImageFiles(paths []string, protectedPaths map[string]bool) cleanupResult {
	var result cleanupResult
	for _, rel := range uniquePathList(paths) {
		if protectedPaths[rel] {
			result.protected++
			continue
		}
		abs, err := a.safeOutputPath(rel)
		if err != nil {
			log.Printf("cleanup path rejected %q: %v", rel, err)
			result.failed++
			continue
		}
		if err := os.Remove(abs); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				result.missing++
			} else {
				log.Printf("cleanup remove failed %s: %v", abs, err)
				result.failed++
				continue
			}
		} else {
			result.deleted++
		}
		removeEmptyParents(abs, a.cfg.output)

		thumbPath, err := a.thumbnailPathForRel(rel)
		if err != nil {
			continue
		}
		if err := os.Remove(thumbPath); err == nil {
			removeEmptyParents(thumbPath, filepath.Join(a.cfg.output, ".thumbnails"))
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("cleanup thumbnail failed %s: %v", thumbPath, err)
		}
	}
	return result
}

func (a *app) thumbnailPathForRel(rel string) (string, error) {
	if _, err := a.safeOutputPath(rel); err != nil {
		return "", err
	}
	return filepath.Join(a.cfg.output, ".thumbnails", filepath.Clean(filepath.FromSlash(rel))), nil
}

func removeEmptyParents(path, root string) {
	root = filepath.Clean(root)
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}

func uniquePathList(paths []string) []string {
	result := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func (j *downloadJob) snapshot() jobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	logs := append([]string(nil), j.Logs...)
	return jobSnapshot{
		ID:        j.ID,
		Status:    j.Status,
		Request:   j.Request,
		Created:   j.Created,
		Started:   j.Started,
		Finished:  j.Finished,
		CleanedAt: j.CleanedAt,
		Found:     j.Found,
		Bytes:     j.Bytes,
		Done:      j.Done,
		Skipped:   j.Skipped,
		Failed:    j.Failed,
		Current:   j.Current,
		Message:   j.Message,
		Error:     j.Error,
		Logs:      logs,
	}
}

func (j *downloadJob) setStatus(status, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = status
	j.Message = message
	if status == "running" && j.Started.IsZero() {
		j.Started = time.Now()
	}
	if status == "done" || status == "failed" || status == "canceled" {
		j.Finished = time.Now()
	}
}

func (j *downloadJob) addLog(format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	message := fmt.Sprintf(format, args...)
	j.Logs = append(j.Logs, message)
	if len(j.Logs) > 100 {
		j.Logs = j.Logs[len(j.Logs)-100:]
	}
	j.Message = message
}

func (j *downloadJob) setError(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "failed"
	j.Error = err.Error()
	j.Message = err.Error()
	j.Finished = time.Now()
}

func (j *downloadJob) cancelJob() {
	j.mu.Lock()
	cancel := j.cancel
	if j.Status == "queued" || j.Status == "running" {
		j.Status = "canceled"
		j.Message = "正在取消"
	}
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (j *downloadJob) addPath(path string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, existing := range j.Paths {
		if existing == path {
			return
		}
	}
	j.Paths = append(j.Paths, path)
}

func (j *downloadJob) markImagesCleaned(at time.Time, result cleanupResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.CleanedAt = at
	message := fmt.Sprintf(
		"图片已自动清理：删除 %d，已不存在 %d，保留 %d",
		result.deleted,
		result.missing,
		result.protected,
	)
	j.Logs = append(j.Logs, message)
	if len(j.Logs) > 100 {
		j.Logs = j.Logs[len(j.Logs)-100:]
	}
	j.Message = message
}

func (a *app) runDownloadJob(ctx context.Context, job *downloadJob, days []time.Time) {
	job.setStatus("running", "开始读取远端目录")
	a.persistJobs()
	items := make([]downloadItem, 0)
	totalFound := 0
	for index, day := range days {
		if ctx.Err() != nil {
			job.setStatus("canceled", "任务已取消")
			return
		}
		files, folderURL, err := a.listDayFiles(ctx, day)
		if err != nil {
			job.addLog("目录失败 %s: %v", folderURL, err)
			continue
		}
		dayItems := filterItems(job.Request, day, folderURL, files)
		if job.Request.Limit > 0 {
			remaining := job.Request.Limit - totalFound
			if remaining <= 0 {
				break
			}
			if len(dayItems) > remaining {
				dayItems = dayItems[:remaining]
			}
		}
		totalFound += len(dayItems)
		items = append(items, dayItems...)
		atomic.StoreInt64(&job.Found, int64(len(items)))
		atomic.AddInt64(&job.Bytes, sumItemBytes(dayItems))
		job.addLog("目录 %d/%d %s -> %d 张", index+1, len(days), day.Format("2006-01-02"), len(dayItems))
		a.persistJobs()
	}

	if ctx.Err() != nil {
		job.setStatus("canceled", "任务已取消")
		a.persistJobs()
		return
	}
	if len(items) == 0 {
		job.setStatus("done", "没有找到匹配图片")
		a.persistJobs()
		return
	}

	job.addLog("开始下载 %d 张图片", len(items))
	workerCount := job.Request.Workers
	if len(items) < workerCount {
		workerCount = len(items)
	}

	jobs := make(chan downloadItem)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				status := a.processItem(ctx, job, item)
				switch status {
				case "downloaded":
					atomic.AddInt64(&job.Done, 1)
				case "skipped":
					atomic.AddInt64(&job.Skipped, 1)
				default:
					atomic.AddInt64(&job.Failed, 1)
				}
				processed := atomic.LoadInt64(&job.Done) + atomic.LoadInt64(&job.Skipped) + atomic.LoadInt64(&job.Failed)
				if processed%20 == 0 || processed == int64(len(items)) {
					job.addLog("进度 %d/%d，下载 %d，跳过 %d，失败 %d", processed, len(items), atomic.LoadInt64(&job.Done), atomic.LoadInt64(&job.Skipped), atomic.LoadInt64(&job.Failed))
					a.persistJobs()
				}
			}
		}()
	}

	for _, item := range items {
		if ctx.Err() != nil {
			break
		}
		jobs <- item
	}
	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		job.setStatus("canceled", "任务已取消")
		a.persistJobs()
		return
	}
	if atomic.LoadInt64(&job.Failed) > 0 {
		job.setStatus("failed", "部分图片下载失败")
		a.persistJobs()
		return
	}
	job.setStatus("done", "下载完成")
	a.persistJobs()
}

func (a *app) listDayFiles(ctx context.Context, day time.Time) ([]remoteFile, string, error) {
	folderURL := fmt.Sprintf("%s/%s/", a.cfg.baseURL, day.Format("2006/01/02"))
	body, err := a.requestBytesWithRetry(ctx, http.MethodGet, folderURL, nil)
	if err != nil {
		return nil, folderURL, err
	}
	files := parseDirectoryListing(string(body))
	sort.Slice(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})
	return files, folderURL, nil
}

func filterItems(input jobInput, day time.Time, folderURL string, files []remoteFile) []downloadItem {
	resolutionSet := makeSet(input.Resolutions)
	channelSet := makeSet(input.Channels)
	items := make([]downloadItem, 0, len(files))
	for _, file := range files {
		match := fileNamePattern.FindStringSubmatch(file.name)
		if match == nil {
			continue
		}
		resolution := match[3]
		channel := match[4]
		if !resolutionSet[resolution] || !channelSet[channel] {
			continue
		}
		items = append(items, downloadItem{
			day:        day,
			resolution: resolution,
			channel:    channel,
			url:        joinURL(folderURL, file.name),
			filename:   file.name,
			size:       file.size,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.resolution != b.resolution {
			return numericLess(a.resolution, b.resolution)
		}
		if a.channel != b.channel {
			return a.channel < b.channel
		}
		return a.filename < b.filename
	})
	return items
}

func (a *app) processItem(ctx context.Context, job *downloadJob, item downloadItem) string {
	relPath := itemRelativePath(item)
	savePath := filepath.Join(a.cfg.output, filepath.FromSlash(relPath))
	job.mu.Lock()
	job.Current = relPath
	job.mu.Unlock()

	complete, err := a.existingFileIsComplete(ctx, savePath, item, job.Request.VerifyExisting)
	if err != nil {
		job.addLog("校验失败 %s: %v", relPath, err)
	}
	if complete {
		job.addPath(relPath)
		return "skipped"
	}
	if err := a.downloadFile(ctx, item, savePath); err != nil {
		job.addLog("下载失败 %s: %v", relPath, err)
		return "failed"
	}
	job.addPath(relPath)
	return "downloaded"
}

func itemRelativePath(item downloadItem) string {
	return strings.Join([]string{
		item.day.Format("2006"),
		item.day.Format("01"),
		item.day.Format("02"),
		item.resolution,
		item.channel,
		item.filename,
	}, "/")
}

func (a *app) existingFileIsComplete(ctx context.Context, savePath string, item downloadItem, verify bool) (bool, error) {
	info, err := os.Stat(savePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !verify {
		return true, nil
	}

	remoteSize, err := a.remoteFileSize(ctx, item.url)
	if err != nil {
		return true, err
	}
	if remoteSize <= 0 || info.Size() == remoteSize {
		return true, nil
	}

	partPath := savePath + ".part"
	if info.Size() < remoteSize {
		_ = os.Remove(partPath)
		if err := os.Rename(savePath, partPath); err != nil {
			return false, err
		}
		return false, nil
	}

	badPath := savePath + ".bad"
	_ = os.Remove(badPath)
	if err := os.Rename(savePath, badPath); err != nil {
		return false, err
	}
	return false, nil
}

func (a *app) remoteFileSize(ctx context.Context, targetURL string) (int64, error) {
	var size int64 = -1
	err := retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", defaultUserAgent)
		resp, err := a.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if isRetryableStatus(resp.StatusCode) {
			return retryableHTTPError(resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("HEAD %s: %s", targetURL, resp.Status)
		}
		size = resp.ContentLength
		return nil
	})
	return size, err
}

func (a *app) downloadFile(ctx context.Context, item downloadItem, savePath string) error {
	if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
		return err
	}
	partPath := savePath + ".part"
	return retry(ctx, func() error {
		resumeAt := int64(0)
		if info, err := os.Stat(partPath); err == nil {
			resumeAt = info.Size()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", defaultUserAgent)
		if resumeAt > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeAt))
		}
		resp, err := a.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && resumeAt > 0 {
			remoteSize := item.size
			if remoteSize <= 0 {
				var sizeErr error
				remoteSize, sizeErr = a.remoteFileSize(ctx, item.url)
				if sizeErr != nil {
					return sizeErr
				}
			}
			if remoteSize > 0 && resumeAt == remoteSize {
				return os.Rename(partPath, savePath)
			}
			_ = os.Remove(partPath)
			return fmt.Errorf("temporary partial reset: range no longer valid")
		}
		if isRetryableStatus(resp.StatusCode) {
			return retryableHTTPError(resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("GET %s: %s", item.url, resp.Status)
		}

		appendMode := resumeAt > 0 && resp.StatusCode == http.StatusPartialContent
		flags := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
			resumeAt = 0
		}
		output, err := os.OpenFile(partPath, flags, 0644)
		if err != nil {
			return err
		}
		defer output.Close()
		written, err := io.CopyBuffer(output, resp.Body, make([]byte, 512*1024))
		if err != nil {
			return err
		}
		if resp.ContentLength >= 0 && written != resp.ContentLength {
			return fmt.Errorf("short download: got %d bytes, expected %d", written, resp.ContentLength)
		}
		if err := output.Close(); err != nil {
			return err
		}
		_ = os.Remove(savePath)
		return os.Rename(partPath, savePath)
	})
}

func (a *app) requestBytesWithRetry(ctx context.Context, method, targetURL string, headers map[string]string) ([]byte, error) {
	var body []byte
	err := retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", defaultUserAgent)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := a.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if isRetryableStatus(resp.StatusCode) {
			return retryableHTTPError(resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("%s %s: %s", method, targetURL, resp.Status)
		}
		body, err = io.ReadAll(resp.Body)
		return err
	})
	return body, err
}

func (a *app) queryImages(values url.Values) ([]imageInfo, int, int, int, error) {
	items, err := a.collectImages(values)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	limit, offset := pageParams(values)
	page := paginateImages(items, limit, offset)
	return page, len(items), limit, offset, nil
}

func (a *app) queryImagesForJob(job *downloadJob, values url.Values) ([]imageInfo, int, int, int, error) {
	items, err := a.collectJobImages(job)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	limit, offset := pageParams(values)
	page := paginateImages(items, limit, offset)
	return page, len(items), limit, offset, nil
}

func (a *app) collectImages(values url.Values) ([]imageInfo, error) {
	fromText := strings.TrimSpace(values.Get("from"))
	toText := strings.TrimSpace(values.Get("to"))
	resolution := strings.TrimSpace(values.Get("resolution"))
	channel := strings.TrimSpace(values.Get("channel"))

	var from, to time.Time
	var err error
	if fromText != "" {
		from, err = parseDay(fromText)
		if err != nil {
			return nil, err
		}
	}
	if toText != "" {
		to, err = parseDay(toText)
		if err != nil {
			return nil, err
		}
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return nil, errors.New("结束日期不能早于开始日期")
	}

	items := make([]imageInfo, 0)
	err = filepath.WalkDir(a.cfg.output, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".thumbnails" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".jpg") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(a.cfg.output, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		imageInfo, ok := buildImageInfo(rel, info)
		if !ok {
			return nil
		}
		if resolution != "" && imageInfo.Resolution != resolution {
			return nil
		}
		if channel != "" && imageInfo.Channel != channel {
			return nil
		}
		day, err := parseDay(imageInfo.Date)
		if err != nil {
			return nil
		}
		if !from.IsZero() && day.Before(from) {
			return nil
		}
		if !to.IsZero() && day.After(to) {
			return nil
		}
		imageInfo.Thumbnail = "/api/thumb?path=" + url.QueryEscape(rel)
		imageInfo.Original = "/api/file?path=" + url.QueryEscape(rel)
		items = append(items, imageInfo)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortImageInfos(items)
	return items, nil
}

func (a *app) collectJobImages(job *downloadJob) ([]imageInfo, error) {
	paths := job.pathsSnapshot()
	if len(paths) > 0 {
		return a.imageInfosFromPaths(paths), nil
	}

	from, err := parseDay(job.Request.From)
	if err != nil {
		return nil, err
	}
	to, err := parseDay(job.Request.To)
	if err != nil {
		return nil, err
	}
	resolutionSet := makeSet(job.Request.Resolutions)
	channelSet := makeSet(job.Request.Channels)
	values := url.Values{}
	values.Set("from", from.Format("2006-01-02"))
	values.Set("to", to.Format("2006-01-02"))
	items, err := a.collectImages(values)
	if err != nil {
		return nil, err
	}
	filtered := items[:0]
	for _, item := range items {
		if resolutionSet[item.Resolution] && channelSet[item.Channel] {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (j *downloadJob) pathsSnapshot() []string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return append([]string(nil), j.Paths...)
}

func (a *app) imageInfosFromPaths(paths []string) []imageInfo {
	items := make([]imageInfo, 0, len(paths))
	seen := make(map[string]bool)
	for _, rel := range paths {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		abs, err := a.safeOutputPath(rel)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		item, ok := buildImageInfo(rel, info)
		if !ok {
			continue
		}
		item.Thumbnail = "/api/thumb?path=" + url.QueryEscape(rel)
		item.Original = "/api/file?path=" + url.QueryEscape(rel)
		items = append(items, item)
	}
	sortImageInfos(items)
	return items
}

func pageParams(values url.Values) (int, int) {
	limit := parseIntDefault(values.Get("limit"), 24)
	if limit < 1 {
		limit = 24
	}
	if limit > maxImageListLimit {
		limit = maxImageListLimit
	}
	offset := parseIntDefault(values.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func paginateImages(items []imageInfo, limit, offset int) []imageInfo {
	if offset >= len(items) {
		return []imageInfo{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

func sortImageInfos(items []imageInfo) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date > items[j].Date
		}
		if items[i].Time != items[j].Time {
			return items[i].Time > items[j].Time
		}
		if items[i].Resolution != items[j].Resolution {
			return numericLess(items[j].Resolution, items[i].Resolution)
		}
		return items[i].Channel < items[j].Channel
	})
}

func buildImageInfo(rel string, info os.FileInfo) (imageInfo, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) != 6 {
		return imageInfo{}, false
	}
	year, month, day := parts[0], parts[1], parts[2]
	resolution, channel, filename := parts[3], parts[4], parts[5]
	match := fileNamePattern.FindStringSubmatch(filename)
	if match == nil {
		return imageInfo{}, false
	}
	dateText := fmt.Sprintf("%s-%s-%s", year, month, day)
	fileDate := match[1]
	if fileDate != year+month+day {
		dateText = fmt.Sprintf("%s-%s-%s", fileDate[0:4], fileDate[4:6], fileDate[6:8])
	}
	timeText := fmt.Sprintf("%s:%s:%s", match[2][0:2], match[2][2:4], match[2][4:6])
	return imageInfo{
		Path:        rel,
		Filename:    filename,
		Date:        dateText,
		Time:        timeText,
		Resolution:  resolution,
		Channel:     channel,
		ChannelName: channelTitle(channel),
		Size:        info.Size(),
		ModifiedAt:  info.ModTime(),
	}, true
}

func (a *app) safeOutputPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("缺少图片路径")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." || filepath.IsAbs(clean) {
		return "", errors.New("图片路径无效")
	}
	abs := filepath.Join(a.cfg.output, clean)
	relative, err := filepath.Rel(a.cfg.output, abs)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("图片路径越界")
	}
	return abs, nil
}

func (a *app) ensureThumbnail(relPath, absPath string) (string, error) {
	sourceInfo, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	thumbRel := filepath.Clean(filepath.FromSlash(relPath))
	thumbPath := filepath.Join(a.cfg.output, ".thumbnails", thumbRel)
	if info, err := os.Stat(thumbPath); err == nil && info.ModTime().After(sourceInfo.ModTime()) {
		return thumbPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0755); err != nil {
		return "", err
	}
	if err := createThumbnail(absPath, thumbPath, defaultThumbMaxDim); err != nil {
		return "", err
	}
	return thumbPath, nil
}

func createThumbnail(srcPath, dstPath string, maxDim int) error {
	input, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer input.Close()
	img, _, err := image.Decode(input)
	if err != nil {
		return err
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return errors.New("invalid image dimensions")
	}
	scale := math.Min(float64(maxDim)/float64(width), float64(maxDim)/float64(height))
	if scale > 1 {
		scale = 1
	}
	dstW := int(math.Round(float64(width) * scale))
	dstH := int(math.Round(float64(height) * scale))
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	thumb := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		srcY := bounds.Min.Y + y*height/dstH
		for x := 0; x < dstW; x++ {
			srcX := bounds.Min.X + x*width/dstW
			thumb.Set(x, y, img.At(srcX, srcY))
		}
	}
	output, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer output.Close()
	return jpeg.Encode(output, thumb, &jpeg.Options{Quality: 78})
}

func addFileToZip(zipWriter *zip.Writer, rel, abs string) error {
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(rel)
	header.Method = zip.Deflate
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}
	input, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer input.Close()
	_, err = io.Copy(writer, input)
	return err
}

func parseDirectoryListing(text string) []remoteFile {
	var files []remoteFile
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		match := linePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name := cleanHref(match[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		files = append(files, remoteFile{name: name, size: parseListedSize(match[2], match[3])})
	}
	for _, match := range hrefPattern.FindAllStringSubmatch(text, -1) {
		name := cleanHref(match[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		files = append(files, remoteFile{name: name, size: -1})
	}
	return files
}

func cleanHref(raw string) string {
	value := html.UnescapeString(raw)
	if unescaped, err := url.PathUnescape(value); err == nil {
		value = unescaped
	}
	if strings.Contains(value, "/") || !strings.HasSuffix(strings.ToLower(value), ".jpg") {
		return ""
	}
	return value
}

func parseListedSize(numberText, unit string) int64 {
	number, err := strconv.ParseFloat(numberText, 64)
	if err != nil {
		return -1
	}
	multiplier := float64(1)
	switch unit {
	case "K":
		multiplier = 1024
	case "M":
		multiplier = 1024 * 1024
	case "G":
		multiplier = 1024 * 1024 * 1024
	}
	return int64(math.Round(number * multiplier))
}

func retry(ctx context.Context, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= 8; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableError(err) || attempt == 8 {
			return err
		}
		delay := retryDelay(2*time.Second, attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

type httpStatusError int

func (e httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d", int(e))
}

func retryableHTTPError(status int) error {
	return httpStatusError(status)
}

func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status == http.StatusInternalServerError ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func isRetryableError(err error) bool {
	var statusErr httpStatusError
	return errors.As(err, &statusErr) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "connection reset") ||
		strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "temporary")
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	delay := base * time.Duration(1<<(attempt-1))
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func joinURL(base, name string) string {
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(name)
}

func sumItemBytes(items []downloadItem) int64 {
	var total int64
	for _, item := range items {
		if item.size > 0 {
			total += item.size
		}
	}
	return total
}

func makeSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func numericLess(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}

func parseDay(text string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(text), time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("日期 %q 无效，应使用 YYYY-MM-DD", text)
	}
	return day, nil
}

func parseIntDefault(text string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return fallback
	}
	return value
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func validateOptions(values, allowed []string, label string) error {
	allowedSet := makeSet(allowed)
	for _, value := range values {
		if !allowedSet[value] {
			return fmt.Errorf("%s %q 不支持", label, value)
		}
	}
	return nil
}

func jobIsActive(status string) bool {
	return status == "queued" || status == "running"
}

func jobIsTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "canceled"
}

func channelTitle(id string) string {
	for _, option := range channelOptions {
		if option.ID == id {
			return option.Title
		}
	}
	return id
}

func randomID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{Error: message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "方法不允许")
}
