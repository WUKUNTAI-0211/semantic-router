package testcases

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

type cacheRunSummary struct {
	results       []CacheResult
	totalRequests int
	cacheHits     int
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

	summary, err := runCacheCases(ctx, testCases, localPort, opts.Verbose)
	if err != nil {
		return err
	}

	// Calculate hit rate for the baseline similar-question probe.
	hitRate := float64(0)
	if summary.totalRequests > 0 {
		hitRate = float64(summary.cacheHits) / float64(summary.totalRequests) * 100
	}

	// Set details for reporting
	if opts.SetDetails != nil {
		opts.SetDetails(map[string]interface{}{
			"total_requests": summary.totalRequests,
			"cache_hits":     summary.cacheHits,
			"cache_misses":   summary.totalRequests - summary.cacheHits,
			"hit_rate":       fmt.Sprintf("%.2f%%", hitRate),
		})
	}

	// Print results
	printCacheResults(summary.results, summary.totalRequests, summary.cacheHits, hitRate)

	if opts.Verbose {
		fmt.Printf("[Test] Cache test completed: %d/%d cache hits (%.2f%% hit rate)\n",
			summary.cacheHits, summary.totalRequests, hitRate)
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
	if err := validateCacheCases(cases); err != nil {
		return nil, err
	}

	return cases, nil
}

func validateCacheCases(cases []CacheTestCase) error {
	if len(cases) == 0 {
		return fmt.Errorf("cache test fixture contains no cases")
	}

	negationControls := 0
	for i, testCase := range cases {
		if strings.TrimSpace(testCase.OriginalQuestion) == "" {
			return fmt.Errorf("cache case %d has an empty original_question", i)
		}
		if len(testCase.SimilarQuestions) == 0 {
			return fmt.Errorf("cache case %d must define similar_questions as a positive cache-hit control", i)
		}
		for _, question := range testCase.SimilarQuestions {
			if strings.TrimSpace(question) == "" {
				return fmt.Errorf("cache case %d has an empty similar_questions entry", i)
			}
		}
		if len(testCase.NegationQuestions) == 0 {
			continue
		}
		negationControls++
		for _, question := range testCase.NegationQuestions {
			if strings.TrimSpace(question) == "" {
				return fmt.Errorf("cache case %d has an empty negation_questions entry", i)
			}
		}
	}

	if negationControls == 0 {
		return fmt.Errorf("cache test fixture must define negation_questions for at least one acceptance case")
	}
	return nil
}

func runCacheCases(ctx context.Context, testCases []CacheTestCase, localPort string, verbose bool) (cacheRunSummary, error) {
	var summary cacheRunSummary
	for _, testCase := range testCases {
		results, err := runCacheCase(ctx, testCase, localPort, verbose)
		if err != nil {
			return cacheRunSummary{}, err
		}
		summary.results = append(summary.results, results...)
		for _, result := range results {
			summary.totalRequests++
			if result.CacheHit {
				summary.cacheHits++
			}
		}
	}
	return summary, nil
}

func runCacheCase(ctx context.Context, testCase CacheTestCase, localPort string, verbose bool) ([]CacheResult, error) {
	if verbose {
		fmt.Printf("[Test] Sending original question: %s\n", testCase.OriginalQuestion)
	}
	if _, err := sendChatRequest(ctx, testCase.OriginalQuestion, localPort, verbose); err != nil {
		if len(testCase.NegationQuestions) == 0 {
			if verbose {
				fmt.Printf("[Test] Error sending original question: %v\n", err)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("polarity guard seed %q: %w", testCase.OriginalQuestion, err)
	}

	// The cache write follows the response path, so allow it to settle before
	// evaluating the positive and negative acceptance controls.
	time.Sleep(time.Second)
	results := runSimilarCacheRequests(ctx, testCase, localPort, verbose)
	if len(testCase.NegationQuestions) == 0 {
		return results, nil
	}
	if err := requireCacheHits(testCase, results); err != nil {
		return nil, err
	}
	if err := requireCacheMisses(ctx, testCase, localPort, verbose); err != nil {
		return nil, err
	}
	return results, nil
}

func runSimilarCacheRequests(ctx context.Context, testCase CacheTestCase, localPort string, verbose bool) []CacheResult {
	results := make([]CacheResult, 0, len(testCase.SimilarQuestions))
	for _, question := range testCase.SimilarQuestions {
		results = append(results, testSingleCacheRequest(ctx, testCase, question, localPort, verbose))
	}
	return results
}

func requireCacheHits(testCase CacheTestCase, results []CacheResult) error {
	for _, result := range results {
		if result.Error != "" {
			return fmt.Errorf("polarity guard positive control %q: %s", result.SimilarQuestion, result.Error)
		}
		if !result.CacheHit {
			return fmt.Errorf("polarity guard positive control %q did not hit the cache for %q",
				result.SimilarQuestion, testCase.OriginalQuestion)
		}
	}
	return nil
}

func requireCacheMisses(ctx context.Context, testCase CacheTestCase, localPort string, verbose bool) error {
	for _, question := range testCase.NegationQuestions {
		result := testSingleCacheRequest(ctx, testCase, question, localPort, verbose)
		if result.Error != "" {
			return fmt.Errorf("polarity guard negative control %q: %s", question, result.Error)
		}
		if result.CacheHit {
			return fmt.Errorf("polarity guard negative control %q served the cached answer for %q",
				question, testCase.OriginalQuestion)
		}
	}
	return nil
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
	printCacheCategoryResults(results)
	printCacheMisses(results)
	printCacheErrors(results)
	fmt.Println(separator + "\n")
}

type cacheCategoryStats struct {
	total int
	hits  int
}

func printCacheCategoryResults(results []CacheResult) {
	categoryStats := make(map[string]cacheCategoryStats)
	for _, result := range results {
		stats := categoryStats[result.Category]
		stats.total++
		if result.CacheHit {
			stats.hits++
		}
		categoryStats[result.Category] = stats
	}
	fmt.Println("\nPer-Category Results:")
	for category, stats := range categoryStats {
		categoryHitRate := float64(stats.hits) / float64(stats.total) * 100
		fmt.Printf("  - %-20s: %d/%d (%.2f%%)\n", category, stats.hits, stats.total, categoryHitRate)
	}
}

func printCacheMisses(results []CacheResult) {
	var misses []CacheResult
	for _, result := range results {
		if !result.CacheHit && result.Error == "" {
			misses = append(misses, result)
		}
	}
	if len(misses) > 0 {
		fmt.Println("\nCache Misses:")
		for _, result := range misses {
			fmt.Printf("  - Original: %s\n", result.OriginalQuestion)
			fmt.Printf("    Similar:  %s\n", result.SimilarQuestion)
			fmt.Printf("    Category: %s\n", result.Category)
		}
	}
}

func printCacheErrors(results []CacheResult) {
	var failures []CacheResult
	for _, result := range results {
		if result.Error != "" {
			failures = append(failures, result)
		}
	}
	if len(failures) > 0 {
		fmt.Println("\nErrors:")
		for _, result := range failures {
			fmt.Printf("  - Question: %s\n", result.SimilarQuestion)
			fmt.Printf("    Error: %s\n", result.Error)
		}
	}
}
