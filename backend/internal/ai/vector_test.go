package ai

import (
	"math"
	"testing"
)

func TestCosineSim(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32 // within epsilon
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0.0},
		{"opposite", []float32{1, 1}, []float32{-1, -1}, -1.0},
		{"parallel scaled", []float32{1, 2, 3}, []float32{2, 4, 6}, 1.0},
		{"partial", []float32{1, 1, 0}, []float32{1, 0, 0}, 0.7071}, // 45 deg
		{"empty", []float32{}, []float32{}, 0.0},
		{"length mismatch", []float32{1, 2}, []float32{1}, 0.0},
		{"zero vector", []float32{0, 0}, []float32{1, 1}, 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CosineSim(tc.a, tc.b)
			if math.Abs(float64(got-tc.want)) > 1e-3 {
				t.Errorf("CosineSim(%v, %v) = %f, want %f", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestParseEmbedding(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		v, err := ParseEmbedding(`[0.1, 0.2, -0.3]`)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(v) != 3 || v[0] != 0.1 || v[2] != -0.3 {
			t.Errorf("got %v", v)
		}
	})
	t.Run("empty string → nil no err", func(t *testing.T) {
		v, err := ParseEmbedding("")
		if err != nil || v != nil {
			t.Errorf("got v=%v err=%v", v, err)
		}
	})
	t.Run("malformed → err", func(t *testing.T) {
		_, err := ParseEmbedding(`not json`)
		if err == nil {
			t.Error("expected error")
		}
	})
}

func TestTopK(t *testing.T) {
	needle := []float32{1, 0}
	haystack := [][]float32{
		{0, 1},   // idx 0, sim 0
		{1, 0},   // idx 1, sim 1 ← best
		{1, 1},   // idx 2, sim 0.707
		{-1, 0},  // idx 3, sim -1
		{0.9, 0}, // idx 4, sim 0.9 ← 2nd
	}
	got := TopK(needle, haystack, 2)
	want := []int{1, 4}
	if len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Errorf("TopK = %v, want %v", got, want)
	}
	// k larger than haystack → returns all
	gotAll := TopK(needle, haystack, 99)
	if len(gotAll) != 5 {
		t.Errorf("expected 5, got %d", len(gotAll))
	}
	// k=0 → empty
	if TopK(needle, haystack, 0) != nil {
		t.Error("k=0 should return nil")
	}
}

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"ABC", "abc"},
		{"  12  ", "12"},
		{"１２", "12"},        // full-width digits
		{"ＡＢ", "ab"},        // full-width letters
		{"12, ", "12"},       // punctuation + space stripped
		{"十二", "十二"},      // CJK preserved
		{"12。", "12"},       // CJK period stripped
		{"1 2\t3", "123"},    // internal whitespace stripped
		{"", ""},             // empty
		{"   ", ""},          // whitespace-only
		{"(12)", "12"},       // parens stripped
		{"Ｘ＝５", "x5"},     // full-width letters fold; ＝ (punct) stripped; ５ → 5
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := NormalizeText(tc.in)
			if got != tc.want {
				t.Errorf("NormalizeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
