package media

import "testing"

// TestIsBitmapSubtitleCodec locks in the bitmap-vs-text codec classification
// that gates the admin "extract subtitle" button. Bitmap codecs (PGS/VOBSUB/DVB)
// cannot be transcoded to WebVTT by ffmpeg and must route through Whisper OCR
// instead; text codecs (mov_text / subrip / ass / webvtt ...) return false and
// are directly extractable.
func TestIsBitmapSubtitleCodec(t *testing.T) {
	bitmap := []string{
		"hdmv_pgs_subtitle", // Blu-ray PGS
		"dvd_subtitle",       // VOBSUB (DVD)
		"dvdsub",             // libavcodec alias
		"dvb_subtitle",       // DVB bitmap subtitles
		"dvbsub",             // alias
		"dvb_teletext",       // DVB teletext (page-based)
		"pgssub",             // another PGS alias
	}
	text := []string{
		"mov_text",  // MP4 text
		"subrip",    // SRT
		"srt",       // SRT alias
		"ass",       // ASS
		"ssa",       // SSA
		"webvtt",    // WebVTT
		"microdvd",
		"sami",
		"realtext",
		"aqtitle",
		"jacosub",
		"hdmv_text_subtitle", // text-based, rare; treated as text
		"",                    // empty — not bitmap
	}

	for _, c := range bitmap {
		if !IsBitmapSubtitleCodec(c) {
			t.Errorf("IsBitmapSubtitleCodec(%q) = false, want true (bitmap)", c)
		}
	}
	for _, c := range text {
		if IsBitmapSubtitleCodec(c) {
			t.Errorf("IsBitmapSubtitleCodec(%q) = true, want false (text)", c)
		}
	}

	// Case-insensitivity: ffprobe returns lowercase codec_name, but the helper
	// must tolerate any casing (defensive — never trust external input casing).
	if !IsBitmapSubtitleCodec("HDMV_PGS_SUBTITLE") {
		t.Errorf("IsBitmapSubtitleCodec is case-sensitive; expected case-insensitive match")
	}
}

// TestIsTransientFFmpegError verifies the network-flake classifier that gates
// retry behavior in RunFFmpegWithRetry / ProbeMedia. Only network-class errors
// are retried; real codec/format errors fail fast so we don't waste 3 attempts
// on a fundamental problem.
func TestIsTransientFFmpegError(t *testing.T) {
	transient := []string{
		"End of file [EOF]",
		"Connection reset by peer",
		"temporary failure in name resolution",
		"Connection refused",
		"Connection timed out",
		"I/O error : whatever",
		// case-insensitive: lowercase variants must also match
		"end of file",
		"connection reset",
	}
	permanent := []string{
		"", // empty
		"Subtitle encoding currently only possible from text to text or bitmap to bitmap",
		"Unknown decoder 'foobar'",
		"At least one output file must be specified",
		"Stream map '0:v' matches no streams",
	}
	for _, s := range transient {
		if !IsTransientFFmpegError(s) {
			t.Errorf("IsTransientFFmpegError(%q) = false, want true (transient)", s)
		}
	}
	for _, s := range permanent {
		if IsTransientFFmpegError(s) {
			t.Errorf("IsTransientFFmpegError(%q) = true, want false (permanent)", s)
		}
	}
}
