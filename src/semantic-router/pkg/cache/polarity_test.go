package cache

import "testing"

// TestPolarityMismatch exercises the lexical polarity guard as a pure function
// (no embedding model required, so it always runs in CI). It asserts that
// negated / antonym variants of a query are flagged as a polarity mismatch,
// while genuine paraphrases and merely-different queries are not.
func TestPolarityMismatch(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		cached   string
		want     bool
	}{
		// --- negation cue on exactly one side (must reject) ---
		{"not-inserted", "Should I commit this change?", "Should I not commit this change?", true},
		{"not-inserted-reverse", "Should I not commit this change?", "Should I commit this change?", true},
		{"require-not-require", "Does this feature require a license?", "Does this feature not require a license?", true},
		{"contraction-nt", "Is the cache enabled?", "Isn't the cache enabled?", true},
		{"contraction-cant", "Can I commit this change?", "Can't I commit this change?", true},
		{"contraction-wont", "Will it retry the request?", "Won't it retry the request?", true},
		{"contraction-doesnt", "Does it require a license?", "Doesn't it require a license?", true},
		{"contraction-aint", "Is the cache enabled?", "Ain't the cache enabled?", true},
		{"contraction-aint-are", "Are the workers ready?", "Ain't the workers ready?", true},
		{"without-cue", "Deploy with a sidecar", "Deploy without a sidecar", true},
		{"never-cue", "Should I retry the request?", "Should I never retry the request?", true},

		// --- antonym swap (must reject) ---
		{"on-off", "How to turn on dark mode?", "How to turn off dark mode?", true},
		{"enable-disable", "How do I enable two-factor authentication?", "How do I disable two-factor authentication?", true},
		{"enabled-disabled", "Is caching enabled in production?", "Is caching disabled in production?", true},
		{"open-closed", "Is port 6379 open by default?", "Is port 6379 closed by default?", true},
		{"active-inactive", "Is the API rate limit currently active?", "Is the API rate limit currently inactive?", true},
		{"increase-decrease", "Can I increase my storage quota?", "Can I decrease my storage quota?", true},

		// --- genuine paraphrase (must NOT reject) ---
		{"paraphrase-password", "How do I reset my password?", "What's the way to reset my password?", false},
		{"paraphrase-2fa", "How to enable two-factor auth?", "How do I turn on 2FA?", false},
		{"paraphrase-logs", "Where are the logs stored?", "What location holds the log files?", false},

		// --- unrelated / merely different (must NOT reject) ---
		{"identical", "How do I enable 2FA?", "How do I enable 2FA?", false},
		{"different-topic", "How do I rotate my API key?", "How do I delete my account?", false},

		// --- unpaired antonym token must NOT reject (only one side has the cue) ---
		{"unpaired-on", "How to turn on dark mode?", "How to turn on light mode?", false},

		// --- surface gate: antonym present but too many other tokens differ ---
		{"antonym-but-far", "How do I add a member to the team?", "How do I remove a member from the team?", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := polarityMismatch(tt.incoming, tt.cached); got != tt.want {
				t.Errorf("polarityMismatch(%q, %q) = %v, want %v", tt.incoming, tt.cached, got, tt.want)
			}
		})
	}
}

func TestTruncateForLog(t *testing.T) {
	short := "short query"
	if got := truncateForLog(short); got != short {
		t.Errorf("truncateForLog(%q) = %q, want unchanged", short, got)
	}

	long := "this is a query that is definitely longer than fifty characters in total"
	got := truncateForLog(long)
	if len(got) != 53 || got[len(got)-3:] != "..." { // 50 chars + "..."
		t.Errorf("truncateForLog(long) = %q (len %d), want 50-char prefix + \"...\"", got, len(got))
	}
}
