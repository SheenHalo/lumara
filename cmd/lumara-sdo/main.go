package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
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
	defaultBaseURL = "https://sdo.gsfc.nasa.gov/assets/img/browse"
	userAgent      = "lumara-sdo-downloader-go/1.0"
)

var (
	commonChannels = []string{
		"0304",
		"0171",
		"0193",
		"0211",
		"0094",
		"0131",
		"0335",
		"1600",
		"1700",
		"HMIIF",
		"HMIBC",
	}
	commonResolutions = []string{"512", "1024", "2048", "4096"}

	fileNamePattern = regexp.MustCompile(`^(\d{8})_(\d{6})_(\d+)_([^/]+)\.jpg$`)
	linePattern     = regexp.MustCompile(`<a href="([^"]+\.jpg)">[^<]*</a>\s+\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}\s+([0-9.]+)([KMG]?)`)
	hrefPattern     = regexp.MustCompile(`href="([^"]+\.jpg)"`)
)

type config struct {
	baseURL        string
	output         string
	fromText       string
	toText         string
	resolutions    []string
	channels       []string
	allFiles       bool
	dryRun         bool
	estimate       bool
	flat           bool
	verifyExisting bool
	limit          int
	workers        int
	retries        int
	timeout        time.Duration
	retryDelay     time.Duration
	chunkSize      int
	logEvery       int
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

type daySummary struct {
	found   int
	bytes   int64
	done    int64
	skipped int64
	failed  int64
}

type totals struct {
	found      int64
	bytes      int64
	downloaded int64
	skipped    int64
	failed     int64
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, days, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	client := &http.Client{Timeout: cfg.timeout}

	fmt.Printf("NASA SDO browse: %s\n", cfg.baseURL)
	fmt.Printf("date range: %s -> %s (%d days)\n", days[0].Format("2006-01-02"), days[len(days)-1].Format("2006-01-02"), len(days))
	if cfg.allFiles {
		fmt.Println("filter: all jpg files")
	} else {
		fmt.Printf("resolutions: %s\n", strings.Join(cfg.resolutions, ","))
		fmt.Printf("channels: %s\n", strings.Join(cfg.channels, ","))
	}
	if cfg.estimate {
		fmt.Println("mode: estimate only")
	} else if cfg.dryRun {
		fmt.Println("mode: dry run")
	} else {
		fmt.Printf("workers: %d\n", cfg.workers)
		fmt.Printf("output: %s\n", cfg.output)
	}
	fmt.Println()

	var total totals
	for dayIndex, day := range days {
		if ctx.Err() != nil {
			break
		}

		files, folderURL, err := listDayFiles(ctx, client, cfg, day)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[dir %d/%d] %s failed: %v\n", dayIndex+1, len(days), folderURL, err)
			continue
		}

		items := filterItems(cfg, day, folderURL, files)
		if cfg.limit > 0 {
			remaining := cfg.limit - int(total.found)
			if remaining <= 0 {
				break
			}
			if len(items) > remaining {
				items = items[:remaining]
			}
		}

		dayBytes := sumItemBytes(items)
		atomic.AddInt64(&total.found, int64(len(items)))
		atomic.AddInt64(&total.bytes, dayBytes)

		fmt.Printf(
			"[dir %d/%d] %s -> %d files, %s\n",
			dayIndex+1,
			len(days),
			folderURL,
			len(items),
			formatBytes(dayBytes),
		)

		if len(items) == 0 {
			continue
		}

		if cfg.estimate {
			continue
		}

		summary := processDay(ctx, client, cfg, items)
		atomic.AddInt64(&total.downloaded, summary.done)
		atomic.AddInt64(&total.skipped, summary.skipped)
		atomic.AddInt64(&total.failed, summary.failed)

		fmt.Printf(
			"[day done] %s downloaded=%d skipped=%d failed=%d\n\n",
			day.Format("2006-01-02"),
			summary.done,
			summary.skipped,
			summary.failed,
		)

		if cfg.limit > 0 && int(total.found) >= cfg.limit {
			fmt.Println("limit reached; stopping early.")
			break
		}
	}

	fmt.Println()
	fmt.Printf("found: %d files, %s listed size\n", total.found, formatBytes(total.bytes))
	if !cfg.estimate {
		fmt.Printf("downloaded: %d, skipped: %d, failed: %d\n", total.downloaded, total.skipped, total.failed)
		fmt.Printf("output: %s\n", mustAbs(cfg.output))
	}
}

func parseArgs(args []string) (config, []time.Time, error) {
	cfg := config{}
	fs := flag.NewFlagSet("lumara-sdo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	resolutionsText := strings.Join(commonResolutions, ",")
	channelsText := strings.Join(commonChannels, ",")

	fs.StringVar(&cfg.baseURL, "base-url", defaultBaseURL, "NASA SDO browse base URL")
	fs.StringVar(&cfg.output, "output", "downloads", "download output directory")
	fs.StringVar(&cfg.output, "o", "downloads", "download output directory")
	fs.StringVar(&cfg.fromText, "from", "", "inclusive start date, YYYY-MM-DD")
	fs.StringVar(&cfg.toText, "to", "", "inclusive end date, YYYY-MM-DD")
	fs.StringVar(&resolutionsText, "resolutions", resolutionsText, "comma separated resolutions")
	fs.StringVar(&resolutionsText, "resolution", resolutionsText, "comma separated resolutions")
	fs.StringVar(&channelsText, "channels", channelsText, "comma separated channels")
	fs.StringVar(&channelsText, "channel", channelsText, "comma separated channels")
	fs.BoolVar(&cfg.allFiles, "all", false, "download every jpg in each browse directory")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "list work without downloading")
	fs.BoolVar(&cfg.estimate, "estimate", false, "estimate count and listed size without downloading")
	fs.BoolVar(&cfg.flat, "flat", false, "store selected files directly in the output directory")
	fs.BoolVar(&cfg.verifyExisting, "verify-existing", false, "check remote Content-Length before skipping existing files")
	fs.IntVar(&cfg.limit, "limit", 0, "maximum files to process")
	fs.IntVar(&cfg.workers, "workers", 4, "parallel download workers")
	fs.IntVar(&cfg.retries, "retries", 8, "max retries for each network operation")
	fs.DurationVar(&cfg.timeout, "timeout", 90*time.Second, "HTTP request timeout")
	fs.DurationVar(&cfg.retryDelay, "retry-delay", 2*time.Second, "initial retry delay")
	fs.IntVar(&cfg.chunkSize, "chunk-size", 512*1024, "download chunk size in bytes")
	fs.IntVar(&cfg.logEvery, "log-every", 100, "progress log interval in files")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return cfg, nil, usageError(err)
	}

	if cfg.workers < 1 {
		return cfg, nil, usageError(errors.New("--workers must be >= 1"))
	}
	if cfg.retries < 1 {
		return cfg, nil, usageError(errors.New("--retries must be >= 1"))
	}
	if cfg.timeout <= 0 {
		return cfg, nil, usageError(errors.New("--timeout must be > 0"))
	}
	if cfg.retryDelay <= 0 {
		return cfg, nil, usageError(errors.New("--retry-delay must be > 0"))
	}
	if cfg.chunkSize < 1 {
		return cfg, nil, usageError(errors.New("--chunk-size must be >= 1"))
	}
	if cfg.logEvery < 1 {
		cfg.logEvery = 100
	}

	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	cfg.resolutions = splitCSV(resolutionsText)
	cfg.channels = splitCSV(channelsText)

	days, err := resolveDays(fs.Args(), cfg.fromText, cfg.toText)
	if err != nil {
		return cfg, nil, usageError(err)
	}

	return cfg, days, nil
}

func usageError(err error) error {
	return fmt.Errorf(`%v

Usage:
  lumara-sdo 2026-05 --output /data/sdo
  lumara-sdo --from 2026-01-01 --to 2026-05-19 --all --output /data/sdo
  lumara-sdo 2026-05-17 --resolutions 512 --channels 0304 --limit 3

Important flags:
  --all                 download every .jpg in the browse directory
  --estimate            only estimate file count and listed size
  --dry-run             list selected files without downloading
  --workers N           parallel downloads, default 4
  --verify-existing     compare existing files with remote Content-Length
`, err)
}

func normalizeArgs(args []string) []string {
	valueFlags := map[string]bool{
		"-o":            true,
		"--output":      true,
		"--base-url":    true,
		"--from":        true,
		"--to":          true,
		"--resolutions": true,
		"--resolution":  true,
		"--channels":    true,
		"--channel":     true,
		"--limit":       true,
		"--workers":     true,
		"--retries":     true,
		"--timeout":     true,
		"--retry-delay": true,
		"--chunk-size":  true,
		"--log-every":   true,
	}

	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, 1)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		flags = append(flags, arg)
		name := arg
		if before, _, ok := strings.Cut(arg, "="); ok {
			name = before
		}
		if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positionals...)
}

func resolveDays(positional []string, fromText, toText string) ([]time.Time, error) {
	if fromText != "" || toText != "" {
		if len(positional) > 0 {
			return nil, errors.New("use either a positional target or --from/--to, not both")
		}
		if fromText == "" || toText == "" {
			return nil, errors.New("--from and --to must be used together")
		}
		from, err := parseDay(fromText)
		if err != nil {
			return nil, err
		}
		to, err := parseDay(toText)
		if err != nil {
			return nil, err
		}
		return daysBetween(from, to)
	}

	if len(positional) != 1 {
		return nil, errors.New("provide one target: YYYY, YYYY-MM, or YYYY-MM-DD")
	}

	target := positional[0]
	switch {
	case regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(target):
		day, err := parseDay(target)
		if err != nil {
			return nil, err
		}
		return []time.Time{day}, nil
	case regexp.MustCompile(`^\d{4}-\d{2}$`).MatchString(target):
		month, err := time.ParseInLocation("2006-01", target, time.UTC)
		if err != nil {
			return nil, err
		}
		return daysBetween(month, month.AddDate(0, 1, -1))
	case regexp.MustCompile(`^\d{4}$`).MatchString(target):
		year, err := strconv.Atoi(target)
		if err != nil {
			return nil, err
		}
		return daysBetween(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC))
	default:
		return nil, errors.New("target must be YYYY, YYYY-MM, or YYYY-MM-DD")
	}
}

func parseDay(text string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", text, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q; expected YYYY-MM-DD", text)
	}
	return day, nil
}

func daysBetween(from, to time.Time) ([]time.Time, error) {
	if to.Before(from) {
		return nil, errors.New("--to must be on or after --from")
	}
	var days []time.Time
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		days = append(days, day)
	}
	return days, nil
}

func splitCSV(text string) []string {
	parts := strings.Split(text, ",")
	values := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values
}

func listDayFiles(ctx context.Context, client *http.Client, cfg config, day time.Time) ([]remoteFile, string, error) {
	folderURL := fmt.Sprintf("%s/%s/", cfg.baseURL, day.Format("2006/01/02"))
	body, err := requestBytesWithRetry(ctx, client, cfg, http.MethodGet, folderURL, nil)
	if err != nil {
		return nil, folderURL, err
	}

	files := parseDirectoryListing(string(body))
	sort.Slice(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})
	return files, folderURL, nil
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

func filterItems(cfg config, day time.Time, folderURL string, files []remoteFile) []downloadItem {
	resolutionSet := makeSet(cfg.resolutions)
	channelSet := makeSet(cfg.channels)
	items := make([]downloadItem, 0, len(files))

	for _, file := range files {
		match := fileNamePattern.FindStringSubmatch(file.name)
		if match == nil {
			if !cfg.allFiles {
				continue
			}
			items = append(items, downloadItem{
				day:        day,
				resolution: "unknown",
				channel:    "unknown",
				url:        joinURL(folderURL, file.name),
				filename:   file.name,
				size:       file.size,
			})
			continue
		}

		resolution := match[3]
		channel := match[4]
		if !cfg.allFiles && (!resolutionSet[resolution] || !channelSet[channel]) {
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

func processDay(ctx context.Context, client *http.Client, cfg config, items []downloadItem) daySummary {
	if cfg.dryRun {
		limit := len(items)
		if limit > 20 {
			limit = 20
		}
		for i := 0; i < limit; i++ {
			fmt.Printf("[dry-run] %s\n", relativePath(cfg, items[i]))
		}
		if len(items) > limit {
			fmt.Printf("[dry-run] ... %d more files\n", len(items)-limit)
		}
		return daySummary{found: len(items)}
	}

	jobs := make(chan downloadItem)
	results := make(chan string)

	var wg sync.WaitGroup
	workerCount := cfg.workers
	if len(items) < workerCount {
		workerCount = len(items)
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				status := processItem(ctx, client, cfg, item)
				results <- status
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, item := range items {
			if ctx.Err() != nil {
				return
			}
			jobs <- item
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var summary daySummary
	summary.found = len(items)
	for status := range results {
		switch status {
		case "downloaded":
			summary.done++
		case "skipped":
			summary.skipped++
		default:
			summary.failed++
		}

		processed := summary.done + summary.skipped + summary.failed
		if processed%int64(cfg.logEvery) == 0 || processed == int64(len(items)) {
			fmt.Printf(
				"  progress %d/%d downloaded=%d skipped=%d failed=%d\n",
				processed,
				len(items),
				summary.done,
				summary.skipped,
				summary.failed,
			)
		}
	}

	return summary
}

func processItem(ctx context.Context, client *http.Client, cfg config, item downloadItem) string {
	savePath := filepath.Join(cfg.output, relativePath(cfg, item))

	complete, err := existingFileIsComplete(ctx, client, cfg, savePath, item)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify failed %s: %v\n", relativePath(cfg, item), err)
	}
	if complete {
		return "skipped"
	}

	if err := downloadFile(ctx, client, cfg, item, savePath); err != nil {
		fmt.Fprintf(os.Stderr, "download failed %s: %v\n", relativePath(cfg, item), err)
		return "failed"
	}
	return "downloaded"
}

func relativePath(cfg config, item downloadItem) string {
	if cfg.flat {
		return item.filename
	}

	return filepath.Join(
		item.day.Format("2006"),
		item.day.Format("01"),
		item.day.Format("02"),
		item.resolution,
		item.channel,
		item.filename,
	)
}

func existingFileIsComplete(ctx context.Context, client *http.Client, cfg config, savePath string, item downloadItem) (bool, error) {
	info, err := os.Stat(savePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !cfg.verifyExisting {
		return true, nil
	}

	remoteSize, err := remoteFileSize(ctx, client, cfg, item.url)
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

func remoteFileSize(ctx context.Context, client *http.Client, cfg config, targetURL string) (int64, error) {
	var size int64 = -1
	err := retry(ctx, cfg, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, targetURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := client.Do(req)
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

func downloadFile(ctx context.Context, client *http.Client, cfg config, item downloadItem, savePath string) error {
	if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
		return err
	}

	partPath := savePath + ".part"
	return retry(ctx, cfg, func() error {
		resumeAt := int64(0)
		if info, err := os.Stat(partPath); err == nil {
			resumeAt = info.Size()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		if resumeAt > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeAt))
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && resumeAt > 0 {
			remoteSize := item.size
			if remoteSize <= 0 {
				var sizeErr error
				remoteSize, sizeErr = remoteFileSize(ctx, client, cfg, item.url)
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

		buffer := make([]byte, cfg.chunkSize)
		written, err := io.CopyBuffer(output, resp.Body, buffer)
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

func requestBytesWithRetry(ctx context.Context, client *http.Client, cfg config, method, targetURL string, headers map[string]string) ([]byte, error) {
	var body []byte
	err := retry(ctx, cfg, func() error {
		req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		resp, err := client.Do(req)
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

func retry(ctx context.Context, cfg config, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= cfg.retries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryableError(err) || attempt == cfg.retries {
			return err
		}

		delay := retryDelay(cfg.retryDelay, attempt)
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

func formatBytes(bytes int64) string {
	if bytes < 0 {
		return "unknown"
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.2f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.2f PiB", value/unit)
}

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
