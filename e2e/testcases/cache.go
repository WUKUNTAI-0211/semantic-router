package testcases

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"

	pkgtestcases "github.com/vllm-project/semantic-router/e2e/pkg/testcases"
)

func init() {
	pkgtestcases.Register("semantic-cache", pkgtestcases.TestCase{
		Description: "Test semantic cache hit rate with similar questions",
		Tags:        []string{"kubernetes", "semantic-cache", "performance"},
		Fn:          testCache,
	})
}

// CacheTestCase represents a test case for cache testing
type CacheTestCase struct {
	Description      string   `json:"description"`
	Category         string   `json:"category"`
	OriginalQuestion string   `json:"original_question"`
	SimilarQuestions []string `json:"similar_questions"`
	// NegationQuestions are opposite-meaning (negated / antonym) variants of
	// OriginalQuestion. The polarity guard (#2691) must NOT serve them the
	// cached answer, so any cache hit on one of these is an acceptance failure.
	NegationQuestions []string `json:"negation_questions,omitempty"`
}

// CacheResult tracks the result of a cache test
type CacheResult struct {
	Description      string
	Category         string
	OriginalQuestion string
	SimilarQuestion  string
	CacheHit         bool
	Error            string
}

func testCache(ctx context.Context, client *kubernetes.Clientset, opts pkgtestcases.TestCaseOptions) error {
	if opts.Verbose {
		fmt.Println("[Test] Testing semantic cache functionality")
	}

	// Setup service connection and get local port
	localPort, stopPortForward, err := setupServiceConnection(ctx, client, opts)
	if err != nil {
		return err
	}
	defer stopPortForward() // Ensure port forwarding is stopped when test completes

	// Load test cases from JSON file
	testCases, err := loadCacheCases("e2e/testcases/testdata/cache_cases.json")
	if err != nil {
		return fmt.Errorf("failed to load test cases: %w", err)
	}

	// Run cache tests
	var results []CacheResult
	var negationFalseHits []string
	totalRequests := 0
	cacheHits := 0

	for _, testCase := range testCases {
		// Send original question first (should not hit cache)
		if opts.Verbose {
			fmt.Printf("[Test] Sending original question: %s\n", testCase.OriginalQuestion)
		}
		_, err := sendChatRequest(ctx, testCase.OriginalQuestion, localPort, opts.Verbose)
		if err != nil {
			if opts.Verbose {
				fmt.Printf("[Test] Error sending original question: %v\n", err)
			}
			continue
		}

		// Wait a bit to ensure cache is populated
		time.Sleep(1 * time.Second)

		// Send similar questions (should hit cache)
		for _, similarQuestion := range testCase.SimilarQuestions {
			totalRequests++
			result := testSingleCacheRequest(ctx, testCase, similarQuestion, localPort, opts.Verbose)
			results = append(results, result)
			if result.CacheHit {
				cacheHits++
			}
		}

		// Send negation / antonym variants (must NOT hit cache): the polarity
		// guard (#2691) must not serve the opposite-meaning cached answer. A
		// cache hit here is an acceptance failure, not just a metric.
		for _, negationQuestion := range testCase.NegationQuestions {
			result := testSingleCacheRequest(ctx, testCase, negationQuestion, localPort, opts.Verbose)
			if result.Error != "" {
				if opts.Verbose {
					fmt.Printf("[Test] Error sending negation question %q: %s\n", negationQuestion, result.Error)
				}
				continue
			}
			if result.CacheHit {
				negationFalseHits = append(negationFalseHits,
					fmt.Sprintf("%q served the cached answer for %q", negationQuestion, testCase.OriginalQuestion))
				if opts.Verbose {
					fmt.Printf("[Test] ✗ NEGATION FALSE-HIT: %s\n", negationQuestion)
				}
			} else if opts.Verbose {
				fmt.Printf("[Test] ✓ Negation correctly missed: %s\n", negationQuestion)
			}
		}
	}

	// Calculate hit rate
	hitRate := float64(0)
	if totalRequests > 0 {
		hitRate = float64(cacheHits) / float64(totalRequests) * 100
	}

	// Set details for reporting
	if opts.SetDetails != nil {
		opts.SetDetails(map[string]interface{}{
			"total_requests": totalRequests,
			"cache_hits":     cacheHits,
			"cache_misses":   totalRequests - cacheHits,
			"hit_rate":       fmt.Sprintf("%.2f%%", hitRate),
		})
	}

	// Print results
	printCacheResults(results, totalRequests, cacheHits, hitRate)

	if opts.Verbose {
		fmt.Printf("[Test] Cache test completed: %d/%d cache hits (%.2f%% hit rate)\n",
			cacheHits, totalRequests, hitRate)
	}

	// A negation/antonym variant being served the cached answer is a
	// correctness failure of the polarity guard (#2691), so fail the test.
	if len(negationFalseHits) > 0 {
		return fmt.Errorf("polarity guard: %d negation/antonym variant(s) incorrectly served a cached answer: %v",
			len(negationFalseHits), negationFalseHits)
	}

	return nil
}

func loadCacheCases(filepath string) ([]CacheTestCase, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read test cases file: %w", err)
	}

	var cases []CacheTestCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("failed to parse test cases: %w", err)
	}

	return cases, nil
}

func testSingleCacheRequest(ctx context.Context, testCase CacheTestCase, question, localPort string, verbose bool) CacheResult {
	result := CacheResult{
		Description:      testCase.Description,
		Category:         testCase.Category,
		OriginalQuestion: testCase.OriginalQuestion,
		SimilarQuestion:  question,
	}

	resp, err := sendChatRequest(ctx, question, localPort, verbose)
	if err != nil {
		result.Error = fmt.Sprintf("failed to send request: %v", err)
		return result
	}
	defer resp.Body.Close()

	// Check for cache hit header
	cacheHitHeader := resp.Header.Get("x-vsr-cache-hit")
	result.CacheHit = (cacheHitHeader == "true")

	if verbose {
		if result.CacheHit {
			fmt.Printf("[Test] ✓ Cache HIT for: %s\n", question)
		} else {
			fmt.Printf("[Test] ✗ Cache MISS for: %s\n", question)
		}
	}

	return result
}

func sendChatRequest(ctx context.Context, question, localPort string, verbose bool) (*http.Response, error) {
	requestBody := map[string]interface{}{
		"model": "MoM",
		"messages": []map[string]string{
			{"role": "user", "content": question},
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%s/v1/chat/completions", localPort)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}

func printCacheResults(results []CacheResult, totalRequests, cacheHits int, hitRate float64) {
	separator := "================================================================================"
	fmt.Println("\n" + separator)
	fmt.Println("CACHE TEST RESULTS")
	fmt.Println(separator)
	fmt.Printf("Total Requests: %d\n", totalRequests)
	fmt.Printf("Cache Hits: %d\n", cacheHits)
	fmt.Printf("Hit Rate: %.2f%%\n", hitRate)
	fmt.Println(separator)

	// Group results by category
	categoryStats := make(map[string]struct {
		total int
		hits  int
	})

	for _, result := range results {
		stats := categoryStats[result.Category]
		stats.total++
		if result.CacheHit {
			stats.hits++
		}
		categoryStats[result.Category] = stats
	}

	// Print per-category results
	fmt.Println("\nPer-Category Results:")
	for category, stats := range categoryStats {
		categoryHitRate := float64(stats.hits) / float64(stats.total) * 100
		fmt.Printf("  - %-20s: %d/%d (%.2f%%)\n", category, stats.hits, stats.total, categoryHitRate)
	}

	// Print cache misses
	missCount := 0
	for _, result := range results {
		if !result.CacheHit && result.Error == "" {
			missCount++
		}
	}

	if missCount > 0 {
		fmt.Println("\nCache Misses:")
		for _, result := range results {
			if !result.CacheHit && result.Error == "" {
				fmt.Printf("  - Original: %s\n", result.OriginalQuestion)
				fmt.Printf("    Similar:  %s\n", result.SimilarQuestion)
				fmt.Printf("    Category: %s\n", result.Category)
			}
		}
	}

	// Print errors
	errorCount := 0
	for _, result := range results {
		if result.Error != "" {
			errorCount++
		}
	}

	if errorCount > 0 {
		fmt.Println("\nErrors:")
		for _, result := range results {
			if result.Error != "" {
				fmt.Printf("  - Question: %s\n", result.SimilarQuestion)
				fmt.Printf("    Error: %s\n", result.Error)
			}
		}
	}

	fmt.Println(separator + "\n")
}
