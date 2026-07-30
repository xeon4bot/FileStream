package routes

import (
	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/stream"
	"EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"html/template"
	"strings"

	"github.com/gotd/td/tg"
	range_parser "github.com/quantumsheep/range-parser"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

//go:embed watch.html
var watchHTML string

//go:embed favicon.png
var faviconBytes []byte

var log *zap.Logger

func (e *allRoutes) LoadHome(r *Route) {
	log = e.log.Named("Stream")
	defer log.Info("Loaded stream route")
	r.Engine.GET("/stream/:messageID", getStreamRoute)
	r.Engine.GET("/stream-remux/:messageID", getStreamRemuxRoute)
	r.Engine.GET("/watch/:messageID", getWatchRoute)
	r.Engine.GET("/subtitles/:messageID/:trackIndex", getSubtitlesRoute)
	r.Engine.GET("/subtitles-list/:messageID", getSubtitlesListRoute)
	r.Engine.GET("/audio-list/:messageID", getAudioListRoute)
	r.Engine.GET("/favicon.ico", func(ctx *gin.Context) {
		ctx.Data(http.StatusOK, "image/png", faviconBytes)
	})
	r.Engine.GET("/favicon.png", func(ctx *gin.Context) {
		ctx.Data(http.StatusOK, "image/png", faviconBytes)
	})
}

func getStreamRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	// Enable CORS for streaming files (specifically needed for WebVTT/SRT subtitles)
	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	ctx.Header("Access-Control-Allow-Headers", "*")
	if r.Method == "OPTIONS" {
		ctx.Status(http.StatusOK)
		return
	}

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()

	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	// Intercept browser page views and redirect to watch page
	if ctx.Query("d") != "true" && strings.Contains(r.Header.Get("Accept"), "text/html") &&
		(strings.Contains(file.MimeType, "video") || strings.Contains(file.MimeType, "audio")) {
		ctx.Redirect(http.StatusFound, fmt.Sprintf("/watch/%d?hash=%s", messageID, authHash))
		return
	}

	// for photo messages
	if file.FileSize == 0 {
		res, err := worker.Client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: file.Location,
			Offset:   0,
			Limit:    1024 * 1024,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, ok := res.(*tg.UploadFile)
		if !ok {
			http.Error(w, "unexpected response", http.StatusInternalServerError)
			return
		}
		fileBytes := result.GetBytes()
		ctx.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", file.FileName))
		if r.Method != "HEAD" {
			ctx.Data(http.StatusOK, file.MimeType, fileBytes)
		}
		return
	}

	ctx.Header("Accept-Ranges", "bytes")
	var start, end int64
	rangeHeader := r.Header.Get("Range")

	if rangeHeader == "" {
		start = 0
		end = file.FileSize - 1
		w.WriteHeader(http.StatusOK)
	} else {
		ranges, err := range_parser.Parse(file.FileSize, r.Header.Get("Range"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		start = ranges[0].Start
		end = ranges[0].End
		ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
		log.Info("Content-Range", zap.Int64("start", start), zap.Int64("end", end), zap.Int64("fileSize", file.FileSize))
		w.WriteHeader(http.StatusPartialContent)
	}

	contentLength := end - start + 1
	mimeType := file.MimeType

	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	ctx.Header("Content-Type", mimeType)
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	disposition := "inline"

	if ctx.Query("d") == "true" {
		disposition = "attachment"
	}

	ctx.Header("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"", disposition, file.FileName))

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil {
			log.Error("Failed to create stream pipe", zap.Error(err))
			return
		}
		defer pipe.Close()
		if _, err := io.CopyN(w, pipe, contentLength); err != nil {
			if !utils.IsClientDisconnectError(err) {
				log.Error("Error while copying stream", zap.Error(err))
			}
		}
	}
}

type EmbeddedSubTrack struct {
	Index    int    `json:"index"`
	Language string `json:"language"`
	Title    string `json:"title"`
}

type FFprobeResult struct {
	Streams []FFprobeStream `json:"streams"`
}

type FFprobeStream struct {
	Index     int               `json:"index"`
	CodecName string            `json:"codec_name"`
	CodecType string            `json:"codec_type"`
	Tags      map[string]string `json:"tags"`
}

type WatchPageData struct {
	FileName        string
	FileSizeStr     string
	DurationStr     string
	ResolutionStr   string
	MimeType        string
	Status          string
	StreamURL       string
	RawStreamURL    string
	RemuxURL        string
	DownloadURL     string
	MessageID       int
	Hash            string
	NeedsAudioRemux bool
}

func probeFile(ctx context.Context, streamURL string) (*FFprobeResult, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=index,codec_name,codec_type:stream_tags=language,title",
		"-of", "json",
		streamURL,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var res FFprobeResult
	if err := json.Unmarshal(output, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func getWatchRoute(ctx *gin.Context) {
	w := ctx.Writer

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()

	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	resolutionStr := "N/A"
	if file.Width > 0 && file.Height > 0 {
		resolutionStr = fmt.Sprintf("%dx%d", file.Width, file.Height)
	}

	rawStreamURL := fmt.Sprintf("/stream/%d?hash=%s", messageID, authHash)
	remuxURL := fmt.Sprintf("/stream-remux/%d?hash=%s&audio=0", messageID, authHash)
	downloadURL := fmt.Sprintf("/stream/%d?hash=%s&d=true", messageID, authHash)

	// Determine if stream needs AAC audio remuxing for browser playback compatibility
	needsAudioRemux := false
	fileNameLower := strings.ToLower(file.FileName)

	if strings.Contains(fileNameLower, "dd5_1") ||
		strings.Contains(fileNameLower, "dd5.1") ||
		strings.Contains(fileNameLower, "ac3") ||
		strings.Contains(fileNameLower, "eac3") ||
		strings.Contains(fileNameLower, "dts") ||
		strings.Contains(fileNameLower, "truehd") ||
		strings.Contains(fileNameLower, "multi") ||
		strings.Contains(fileNameLower, "dual") ||
		strings.HasSuffix(fileNameLower, ".mkv") {
		needsAudioRemux = true
	}

	// Probe video streams via FFprobe to verify audio tracks & codecs
	localStreamURL := fmt.Sprintf("http://127.0.0.1:%d/stream/%d?hash=%s", config.ValueOf.Port, messageID, authHash)
	probeCtx, cancel := context.WithTimeout(ctx.Request.Context(), 3*time.Second)
	defer cancel()

	if probeRes, err := probeFile(probeCtx, localStreamURL); err == nil {
		audioCount := 0
		for _, s := range probeRes.Streams {
			if s.CodecType == "audio" {
				audioCount++
				codec := strings.ToLower(s.CodecName)
				if codec == "ac3" || codec == "eac3" || codec == "dts" || codec == "truehd" || codec == "dca" || codec == "mlp" || codec == "flac" {
					needsAudioRemux = true
				}
			}
		}
		if audioCount > 1 {
			needsAudioRemux = true
		}
	}

	streamURL := rawStreamURL

	data := WatchPageData{
		FileName:        file.FileName,
		FileSizeStr:     utils.FormatBytes(file.FileSize),
		DurationStr:     utils.FormatDuration(file.Duration),
		ResolutionStr:   resolutionStr,
		MimeType:        file.MimeType,
		Status:          "Ready",
		StreamURL:       streamURL,
		RawStreamURL:    rawStreamURL,
		RemuxURL:        remuxURL,
		DownloadURL:     downloadURL,
		MessageID:       messageID,
		Hash:            authHash,
		NeedsAudioRemux: needsAudioRemux,
	}

	tmpl, err := template.New("watch").Parse(watchHTML)
	if err != nil {
		log.Error("Failed to parse watch template", zap.Error(err))
		http.Error(w, "internal server error: failed to parse template", http.StatusInternalServerError)
		return
	}

	ctx.Header("Content-Type", "text/html; charset=utf-8")
	ctx.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	ctx.Header("Pragma", "no-cache")
	ctx.Header("Expires", "0")
	w.WriteHeader(http.StatusOK)
	if err := tmpl.Execute(w, data); err != nil {
		log.Error("Failed to render watch template", zap.Error(err))
	}
}

func getSubtitlesRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	trackIndexParm := ctx.Param("trackIndex")
	trackIndex, err := strconv.Atoi(trackIndexParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	ctx.Header("Content-Type", "text/vtt; charset=utf-8")
	ctx.Header("Access-Control-Allow-Origin", "*")

	pipe, err := stream.NewStreamPipe(r.Context(), worker.Client, file.Location, 0, file.FileSize-1, log)
	if err != nil {
		log.Error("Failed to create stream pipe for subtitles", zap.Error(err))
		return
	}
	defer pipe.Close()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(r.Context(), "ffmpeg",
		"-i", "pipe:0",
		"-map", fmt.Sprintf("0:%d", trackIndex),
		"-f", "webvtt",
		"-",
	)

	cmd.Stdin = pipe
	cmd.Stdout = w
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Error("Failed to extract subtitles using ffmpeg pipe", 
			zap.Error(err),
			zap.String("stderr", stderr.String()),
		)
	}
}

func getSubtitlesListRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	localStreamURL := fmt.Sprintf("http://127.0.0.1:%d/stream/%d?hash=%s", config.ValueOf.Port, messageID, authHash)
	probeCtx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	var embeddedSubs []EmbeddedSubTrack = []EmbeddedSubTrack{}
	if probeRes, err := probeFile(probeCtx, localStreamURL); err == nil {
		for _, stream := range probeRes.Streams {
			if stream.CodecType == "subtitle" {
				lang := strings.ToLower(stream.Tags["language"])
				title := strings.ToLower(stream.Tags["title"])
				
				// Keep only English subtitle tracks
				isEnglish := lang == "eng" || lang == "en" || 
					strings.Contains(title, "english") || strings.Contains(title, "eng")
				
				if isEnglish {
					embeddedSubs = append(embeddedSubs, EmbeddedSubTrack{
						Index:    stream.Index,
						Language: stream.Tags["language"],
						Title:    stream.Tags["title"],
					})
				}
			}
		}
	} else {
		log.Warn("Failed to probe stream metadata for subtitles", zap.Error(err))
	}

	ctx.JSON(http.StatusOK, embeddedSubs)
}

func getAudioListRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	localStreamURL := fmt.Sprintf("http://127.0.0.1:%d/stream/%d?hash=%s", config.ValueOf.Port, messageID, authHash)
	probeCtx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	var embeddedAudios []EmbeddedSubTrack = []EmbeddedSubTrack{}
	if probeRes, err := probeFile(probeCtx, localStreamURL); err == nil {
		for _, stream := range probeRes.Streams {
			if stream.CodecType == "audio" {
				embeddedAudios = append(embeddedAudios, EmbeddedSubTrack{
					Index:    stream.Index,
					Language: stream.Tags["language"],
					Title:    stream.Tags["title"],
				})
			}
		}
	} else {
		log.Warn("Failed to probe stream metadata for audios", zap.Error(err))
	}

	ctx.JSON(http.StatusOK, embeddedAudios)
}

// getStreamRemuxRoute remuxes the video stream via FFmpeg selecting a specific audio track.
// Query params: hash (required), audio (0-based relative audio track index), start (seek seconds).
func getStreamRemuxRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	authHash := ctx.Query("hash")
	if authHash == "" {
		http.Error(w, "missing hash param", http.StatusBadRequest)
		return
	}

	audioIndex := ctx.DefaultQuery("audio", "0")
	startSec := ctx.DefaultQuery("start", "0")

	worker := bot.GetNextWorker()
	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expectedHash := utils.PackFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
	)
	if !utils.CheckHash(authHash, expectedHash) {
		http.Error(w, "invalid hash", http.StatusBadRequest)
		return
	}

	localStreamURL := fmt.Sprintf("http://127.0.0.1:%d/stream/%d?hash=%s", config.ValueOf.Port, messageID, authHash)

	// Build FFmpeg args.
	// Key tuning:
	//  -probesize 500000     : Probe only 500 KB instead of the default 5 MB so FFmpeg
	//                          starts producing output in ~1-2 s rather than 20-30 s.
	//  -analyzeduration 500000 : Analyse at most 0.5 s of stream data before starting output.
	//  -fflags +nobuffer+genpts: Flush output immediately; regenerate missing/bad PTS from MKV.
	//  -avoid_negative_ts make_zero : Fix non-monotonic timestamps that can crash the muxer.
	//  -max_interleave_delta 0      : Write packets immediately without interleave buffering.
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-y",
		"-fflags", "+nobuffer+genpts",
		"-probesize", "500000",
		"-analyzeduration", "500000",
	}
	if startSec != "0" && startSec != "" {
		args = append(args, "-ss", startSec)
	}
	args = append(args,
		"-i", localStreamURL,
		"-map", "0:v:0",
		"-map", "0:a:"+audioIndex,
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ac", "2",
		"-avoid_negative_ts", "make_zero",
		"-max_interleave_delta", "0",
		"-f", "mp4",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"pipe:1",
	)

	ctx.Header("Content-Type", "video/mp4")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	cmd := exec.CommandContext(r.Context(), "ffmpeg", args...)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Only log if there's actual stderr output.
		// An empty stderr + non-zero exit usually means the browser closed the
		// connection and the OS killed FFmpeg (expected, not an error).
		if stderrStr := stderr.String(); stderrStr != "" {
			log.Warn("FFmpeg remux error", zap.Error(err), zap.String("stderr", stderrStr))
		}
	}
}

