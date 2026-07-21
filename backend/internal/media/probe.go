package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"studyquest/backend/internal/model"
)

// probeMedia shells out to ffprobe to extract container-level metadata from a
// remote URL. Because the netdisk CDNs honor HTTP Range, ffprobe only reads the
// header/index region (typically <1s). A 30s timeout guards against the
// occasional file that makes ffprobe hang.
//
// Retries up to 3 times on transient TLS errors — the天翼云 OBS CDN
// (obs-ynkmfy-suzhou-home.obs.cn-jssz1.ctyun.cn, ~80% of lesson videos)
// intermittently RSTs ffprobe's TLS connections; single-shot success is only
// ~40% in A/B testing, but 3 attempts with backoff reaches ~95%.
func ProbeMedia(url string) (*model.MediaMeta, error) {
	args := []string{
		"-v", "error",
		"-show_format", "-show_streams",
		"-of", "json",
		url,
	}
	var out []byte
	var lastErr error
	var lastStderr string
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, "ffprobe", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		o, err := cmd.Output()
		cancel()
		if err == nil {
			out = o
			lastErr = nil
			break
		}
		lastErr = err
		lastStderr = stderr.String()
		if ctx.Err() == context.DeadlineExceeded {
			lastErr = errors.New("ffprobe timed out after 30s")
			break // timeout is not transient — don't retry
		}
		if !IsTransientFFmpegError(lastStderr) {
			break // real codec/format error — don't retry
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("ffprobe failed: %w, stderr: %s", lastErr, lastStderr)
	}

	// ffprobe JSON structure (only the fields we consume).
	var probe struct {
		Format struct {
			Duration   string `json:"duration"`
			BitRate    string `json:"bit_rate"`
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			Index       int    `json:"index"`
			CodecType   string `json:"codec_type"`
			CodecName   string `json:"codec_name"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			BitRate     string `json:"bit_rate"`
			AvgFrameRate string `json:"avg_frame_rate"`
			Channels    int    `json:"channels"`
			Tags        struct {
				Language string `json:"language"`
			} `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}

	meta := &model.MediaMeta{
		FormatName: probe.Format.FormatName,
	}
	if d, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
		meta.DurationSeconds = int(d)
	}
	if br, err := strconv.ParseInt(probe.Format.BitRate, 10, 64); err == nil {
		meta.BitRate = br
	}

	for _, s := range probe.Streams {
		ms := model.MediaStream{
			Index:    s.Index,
			Type:     s.CodecType,
			Codec:    s.CodecName,
			Width:    s.Width,
			Height:   s.Height,
			Channels: s.Channels,
			Language: s.Tags.Language,
		}
		// For subtitle streams, flag bitmap-based codecs (PGS/VOBSUB/DVB) so the
		// admin UI can disable the extract button and point the user at Whisper.
		// ffmpeg refuses to transcode these to WebVTT with "Subtitle encoding
		// currently only possible from text to text or bitmap to bitmap".
		if s.CodecType == "subtitle" {
			ms.IsBitmap = IsBitmapSubtitleCodec(s.CodecName)
		}
		if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil {
			ms.BitRate = br
		}
		meta.Streams = append(meta.Streams, ms)

		// Promote the first video/audio stream to top-level convenience
		// fields so consumers don't have to scan the stream list.
		if s.CodecType == "video" && meta.VideoCodec == "" {
			meta.VideoCodec = s.CodecName
			meta.Width = s.Width
			meta.Height = s.Height
			meta.Fps = s.AvgFrameRate
		} else if s.CodecType == "audio" && meta.AudioCodec == "" {
			meta.AudioCodec = s.CodecName
			meta.AudioChannels = s.Channels
		}
	}
	return meta, nil
}
// isBitmapSubtitleCodec reports whether a codec name identifies a
// bitmap/picture-based subtitle format. These cannot be transcoded to a text
// format (WebVTT/SRT) by ffmpeg — it errors with "Subtitle encoding currently
// only possible from text to text or bitmap to bitmap". For such streams the
// only path to text is OCR (Whisper / SubtitleEdit), so the admin UI refuses
// extraction and points the user at Whisper transcription instead.
//
// Covered codecs (case-insensitive ffprobe codec_name):
//   - hdmv_pgs_subtitle  — Blu-ray PGS
//   - dvd_subtitle       — VOBSUB (DVD)
//   - dvb_subtitle       — DVB (digital TV)
//   - dvb_teletext       — DVB teletext
//   - hdmv_text_subtitle — technically text, but rare/fragile; treated as text
//
// Text-based codecs (mov_text / subrip / srt / ass / ssa / webvtt / microdvd /
// sami / realtext / aqTitle / jacosub) return false and ARE extractable.
func IsBitmapSubtitleCodec(codecName string) bool {
	switch strings.ToLower(codecName) {
	case "hdmv_pgs_subtitle", // Blu-ray PGS
		"dvd_subtitle",        // VOBSUB (DVD)
		"dvdsub",              // libavcodec short name alias
		"dvb_subtitle",        // DVB bitmap subtitles
		"dvbsub",              // alias
		"dvb_teletext",        // DVB teletext (page-based, not VTT-able)
		"pgssub":              // another PGS alias
		return true
	}
	return false
}
