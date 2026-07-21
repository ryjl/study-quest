package subtitle

import "strings"

// SrtToVtt converts an SRT subtitle string to WebVTT format.
//
// The two formats are nearly identical. The only mechanical differences are:
//   - WebVTT starts with the literal header "WEBVTT\n\n"
//   - WebVTT uses '.' as the timestamp millisecond separator, SRT uses ','
//
// No cue-body content is altered. Callers that receive raw SRT (the whisper
// worker upload, the admin manual upload, auto-matched files on disk) call
// this to normalize into the single storage format.
//
// Lossless: round-tripping back through VttToSrt recovers the original text
// (timestamp format aside). Inline <b>/<i>/<c.xxx> tags are NOT stripped here —
// they pass through verbatim, since SRT inputs from whisper never contain them
// and SRT inputs from disk that contain them are vanishingly rare (SRT has no
// spec for styling tags; they're a non-standard extension some tools emit).
func SrtToVtt(srt string) string {
	srt = strings.TrimSpace(srt)
	if srt == "" {
		// Still emit a valid (empty) VTT document so downstream code never has to
		// distinguish "no subtitles" from "malformed subtitles".
		return "WEBVTT\n\n"
	}
	return "WEBVTT\n\n" + strings.ReplaceAll(srt, ",", ".")
}

// VttToSrt converts a WebVTT subtitle string to SRT for AI consumption.
//
// This is LOSSY for styling but lossless for text + timing:
//   - Drops the "WEBVTT" header and NOTE/STYLE/REGION blocks
//   - Strips cue settings (e.g. "line:50% align:center" after the timestamp)
//   - Strips inline style tags (<b>, <i>, <u>, <c.class>, <v speaker>,
//     <00:00:01.000> timing tags) keeping only the inner text
//   - Replaces '.' with ',' in timestamp lines (SRT convention)
//
// The AI doesn't care about styling — it just needs the text + timing. Feeding
// it raw VTT would make the segmenter's chunk boundaries noisy with tag chars
// and pollute the LLM prompt with formatting. Converting to plain SRT keeps the
// existing ParseSRT/segmenter/prompt code untouched.
func VttToSrt(vtt string) string {
	vtt = strings.TrimSpace(vtt)
	if vtt == "" {
		return ""
	}
	// Strip UTF-8 BOM if present (some Windows-produced VTT files have one).
	vtt = strings.TrimPrefix(vtt, "\ufeff")
	// Normalize line endings so the CRLF / CR branches below are the only ones
	// we need to reason about during splitting.
	vtt = strings.ReplaceAll(vtt, "\r\n", "\n")
	vtt = strings.ReplaceAll(vtt, "\r", "\n")

	lines := strings.Split(vtt, "\n")
	var out []string
	sawFirstCue := false // flipped once we encounter the first non-header line
	inSkipBlock := false // inside NOTE/STYLE/REGION block (skip until blank line)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)

		// If we're inside a NOTE/STYLE/REGION block, skip everything until the
		// closing blank line. This handles blocks both in the header and mid-body.
		if inSkipBlock {
			if line == "" {
				inSkipBlock = false
			}
			continue
		}

		// A NOTE/STYLE/REGION block starts. These run until the next blank line.
		if strings.HasPrefix(line, "NOTE") ||
			strings.HasPrefix(line, "STYLE") ||
			strings.HasPrefix(line, "REGION") {
			inSkipBlock = true
			continue
		}

		// Header phase (before the first real cue): skip "WEBVTT" + blank lines.
		// Anything else ends the header and is processed as cue content below.
		if !sawFirstCue {
			if line == "" || strings.HasPrefix(line, "WEBVTT") {
				continue
			}
			sawFirstCue = true
			// Fall through: process this line as cue content.
		}

		// Timestamp line: "00:00:01.000 --> 00:00:04.000 [cue settings...]"
		// Strip any trailing cue settings, then flip '.' → ',' for SRT.
		if strings.Contains(line, "-->") {
			line = stripCueSettings(line)
			line = strings.ReplaceAll(line, ".", ",")
			out = append(out, line)
			continue
		}

		// Text line: strip inline style/voice/timing tags.
		out = append(out, stripInlineTags(line))
	}

	result := strings.Join(out, "\n")
	// Collapse 3+ consecutive newlines to exactly 2 (SRT's cue separator).
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

// stripCueSettings removes the optional cue settings that follow the second
// timestamp on a VTT cue header. Input shape (after the arrow):
//
//	"00:00:01.000 --> 00:00:04.000 line:50% align:center size:80%"
//
// becomes:
//
//	"00:00:01.000 --> 00:00:04.000"
//
// We only keep the arrow + the second timestamp token. Anything after the
// first whitespace following the second timestamp is treated as settings.
func stripCueSettings(line string) string {
	arrowIdx := strings.Index(line, "-->")
	if arrowIdx < 0 {
		return line
	}
	rest := strings.TrimSpace(line[arrowIdx+len("-->"):])
	if rest == "" {
		return line
	}
	// rest looks like "00:00:04.000 line:50% align:center" — keep only the
	// first whitespace-separated token (the end timestamp).
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		rest = rest[:sp]
	}
	return line[:arrowIdx+len("-->")] + " " + rest
}

// stripInlineTags removes every <...> tag from s, preserving the inner text.
// Used by VttToSrt to clean text lines: VTT allows inline styling
// (<b>, <i>, <u>), class hooks (<c.red>), voice labels (<v Alice>), and
// in-band timing tags (<00:00:01.000>) — none of which the AI cares about.
//
// Unknown/broken tag shapes are handled conservatively: a '<' with no matching
// '>' leaves the remainder of the line intact (treated as literal text, which
// is what most players do too).
func stripInlineTags(s string) string {
	// Fast path: most text lines have no tags at all. Skip the rune loop.
	if !strings.ContainsRune(s, '<') {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}
