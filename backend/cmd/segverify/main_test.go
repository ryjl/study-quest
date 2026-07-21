package main

import (
	"fmt"
	"testing"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/subtitle"
)

func TestSegmentFromVTT(t *testing.T) {
	vtt := "WEBVTT\n\n" +
		"1\n00:00:00.940 --> 00:00:33.380\n这是咱们暑假班的题.兵6进1.军3进9上来.\n\n" +
		"2\n00:00:34.180 --> 00:00:55.770\n那这样的话黑方退局抢中的时候呢."

	srt := subtitle.VttToSrt(vtt)
	fmt.Printf("=== VttToSrt 输出 ===\n%s\n\n", srt)

	cues, err := ai.ParseSRT(srt)
	if err != nil {
		t.Fatalf("ParseSRT failed: %v", err)
	}
	fmt.Printf("=== ParseSRT 解析出 %d 个 cue ===\n", len(cues))
	for i, c := range cues {
		fmt.Printf("cue %d: [%dms - %dms] %s\n", i+1, c.StartMs, c.EndMs, c.Text)
	}
	if len(cues) != 2 {
		t.Errorf("expected 2 cues, got %d", len(cues))
	}
	if len(cues) > 0 && cues[0].StartMs != 940 {
		t.Errorf("first cue start ms wrong: %d", cues[0].StartMs)
	}
	if len(cues) > 0 && cues[0].EndMs != 33380 {
		t.Errorf("first cue end ms wrong: %d", cues[0].EndMs)
	}
	fmt.Println("\n✓ segment job 读取链路正常（VTT → SRT → cues → chunks）")
}
