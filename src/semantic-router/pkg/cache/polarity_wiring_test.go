//go:build !windows && cgo

package cache

import (
	"testing"
	"time"
)

// TestFinishFindSimilarSearchPolarityWiring verifies the guard is actually
// wired into the in-memory lookup decision, independent of any embedding model.
// It calls finishFindSimilarSearch directly with a synthetic above-threshold
// candidate, so it always runs in CI (cgo build, no model fetch required) and
// deterministically proves that a polarity mismatch flips a would-be hit into a
// miss while a genuine match is still served. The model-dependent end-to-end
// behavior over real mmBERT embeddings is covered separately (and skipped when
// the model is absent) in negation_regression_test.go.
func TestFinishFindSimilarSearchPolarityWiring(t *testing.T) {
	const threshold = float32(0.80)
	const aboveThreshold = float32(0.95)

	newCacheWithEntry := func() (*InMemoryCache, CacheEntry) {
		c := NewInMemoryCache(InMemoryCacheOptions{
			SimilarityThreshold: threshold,
			MaxEntries:          16,
			TTLSeconds:          0, // keep updateAccessInfo off the expiration heap
			Enabled:             true,
			EvictionPolicy:      FIFOEvictionPolicyType,
		})
		entry := CacheEntry{
			RequestID:    "e1",
			Model:        "model-x",
			Query:        "How do I enable two-factor authentication?",
			ResponseBody: []byte("ENABLE-ANSWER"),
			Timestamp:    time.Now(),
		}
		c.entries = append(c.entries, entry)
		return c, entry
	}

	t.Run("polarity mismatch flips above-threshold candidate to miss", func(t *testing.T) {
		c, entry := newCacheWithEntry()
		body, hit, err := c.finishFindSimilarSearch(
			time.Now(), "model-x",
			"How do I disable two-factor authentication?", // antonym of the cached query
			threshold, 0, entry, aboveThreshold, 1, 0,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hit || body != nil {
			t.Fatalf("expected miss on polarity mismatch, got hit=%v body=%q", hit, string(body))
		}
		if got := c.LastSimilarity(); got != aboveThreshold {
			t.Errorf("LastSimilarity = %.4f, want %.4f (similarity must still be recorded on reject)", got, aboveThreshold)
		}
	})

	t.Run("genuine match above threshold is still served", func(t *testing.T) {
		c, entry := newCacheWithEntry()
		body, hit, err := c.finishFindSimilarSearch(
			time.Now(), "model-x",
			"How do I enable two-factor authentication?", // identical to the cached query
			threshold, 0, entry, aboveThreshold, 1, 0,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hit || string(body) != "ENABLE-ANSWER" {
			t.Fatalf("expected hit returning the cached answer, got hit=%v body=%q", hit, string(body))
		}
	})
}
