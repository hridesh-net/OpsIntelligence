// Package cveclient queries public vulnerability databases for known CVEs
// affecting a given package@version. The default backend is OSV.dev — a free,
// no-auth, well-maintained aggregate of npm advisories, GHSA, PyPA, RustSec,
// and others — so Repo Intelligence stops asking the LLM to recall CVEs from
// training data and instead grounds the scan in a live feed.
package cveclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OSVEndpoint is the public OSV.dev query endpoint.
const OSVEndpoint = "https://api.osv.dev/v1/query"

// Vulnerability is the subset of an OSV record we surface to the scanner.
// The full OSV schema is huge; we keep only the fields the scan UI renders
// plus enough to round-trip into a ScanResult CVEFinding.
type Vulnerability struct {
	ID            string   `json:"id"`              // e.g. "GHSA-xxxx-xxxx-xxxx" or "CVE-2024-1234"
	Summary       string   `json:"summary"`         // one-line description
	Details       string   `json:"details"`         // longer markdown description
	Severity      string   `json:"severity"`        // critical|high|medium|low (normalised)
	Aliases       []string `json:"aliases"`         // cross-references (CVE-… GHSA-…)
	References    []string `json:"references"`      // advisory URLs
	FixedVersions []string `json:"fixed_versions"`  // versions that contain the patch
	Ecosystem     string   `json:"ecosystem"`       // e.g. "Go", "PyPI", "npm"
	Package       string   `json:"package"`
	Affected      string   `json:"affected_version"` // the version we queried
}

// Client queries an OSV-compatible endpoint.
type Client struct {
	HTTP     *http.Client
	Endpoint string
}

// NewClient returns a Client with a sane HTTP timeout.
func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 12 * time.Second},
		Endpoint: OSVEndpoint,
	}
}

// EcosystemFor maps a free-text language label (as stored in RepoMemory.PrimaryLang)
// to the OSV ecosystem slug. Returns "" when the language has no known mapping.
//
// We support the languages the indexer can confidently detect today; adding more
// is a one-line change. Unknown languages fall through to the LLM-only path.
func EcosystemFor(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return "Go"
	case "python", "py":
		return "PyPI"
	case "rust", "rs":
		return "crates.io"
	case "javascript", "typescript", "js", "ts", "node", "nodejs":
		return "npm"
	case "ruby", "rb":
		return "RubyGems"
	case "java", "kotlin":
		return "Maven"
	case "c#", "csharp", "dotnet":
		return "NuGet"
	case "php":
		return "Packagist"
	case "elixir":
		return "Hex"
	case "haskell":
		return "Hackage"
	case "dart", "flutter":
		return "Pub"
	default:
		return ""
	}
}

// osvQueryReq matches the OSV API's POST body schema.
//
// Reference: https://google.github.io/osv.dev/post-v1-query/
type osvQueryReq struct {
	Version string `json:"version,omitempty"`
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
}

// osvVuln is the relevant subset of OSV's response Vulnerability schema.
type osvVuln struct {
	ID         string   `json:"id"`
	Summary    string   `json:"summary"`
	Details    string   `json:"details"`
	Aliases    []string `json:"aliases"`
	References []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"references"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"` // GHSA gives plain "HIGH" / "MEDIUM" here
	} `json:"database_specific"`
	Affected []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Events []struct {
				Introduced string `json:"introduced,omitempty"`
				Fixed      string `json:"fixed,omitempty"`
			} `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

// QueryPackage returns the vulnerabilities affecting (pkg, version) in
// ecosystem. An empty version still works — OSV returns all known CVEs.
//
// Network or 5xx errors return an error; a 404/empty result returns (nil, nil).
func (c *Client) QueryPackage(ctx context.Context, ecosystem, pkg, version string) ([]Vulnerability, error) {
	if ecosystem == "" || pkg == "" {
		return nil, nil
	}

	var req osvQueryReq
	req.Version = version
	req.Package.Name = pkg
	req.Package.Ecosystem = ecosystem

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("osv: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("osv: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("osv: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("osv: server error %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("osv: status %d: %s", resp.StatusCode, raw)
	}

	var out struct {
		Vulns []osvVuln `json:"vulns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("osv: decode: %w", err)
	}

	results := make([]Vulnerability, 0, len(out.Vulns))
	for _, v := range out.Vulns {
		results = append(results, toVulnerability(v, ecosystem, pkg, version))
	}
	return results, nil
}

func toVulnerability(v osvVuln, ecosystem, pkg, version string) Vulnerability {
	out := Vulnerability{
		ID:        v.ID,
		Summary:   v.Summary,
		Details:   v.Details,
		Aliases:   v.Aliases,
		Ecosystem: ecosystem,
		Package:   pkg,
		Affected:  version,
		Severity:  normaliseSeverity(v),
	}
	for _, r := range v.References {
		if r.URL != "" {
			out.References = append(out.References, r.URL)
		}
	}
	// Collect fixed versions across all affected ranges.
	seen := map[string]struct{}{}
	for _, aff := range v.Affected {
		for _, rng := range aff.Ranges {
			for _, ev := range rng.Events {
				if ev.Fixed == "" {
					continue
				}
				if _, ok := seen[ev.Fixed]; ok {
					continue
				}
				seen[ev.Fixed] = struct{}{}
				out.FixedVersions = append(out.FixedVersions, ev.Fixed)
			}
		}
	}
	return out
}

// normaliseSeverity collapses OSV's heterogenous severity into our four-tier
// scale. OSV records sometimes carry a CVSS score, sometimes a GHSA-style
// "HIGH"/"MEDIUM" string, and sometimes nothing at all — handle each.
func normaliseSeverity(v osvVuln) string {
	if v.DatabaseSpecific.Severity != "" {
		switch strings.ToLower(v.DatabaseSpecific.Severity) {
		case "critical":
			return "critical"
		case "high":
			return "high"
		case "moderate", "medium":
			return "medium"
		case "low":
			return "low"
		}
	}
	for _, s := range v.Severity {
		if s.Type == "CVSS_V3" || s.Type == "CVSS_V4" {
			// Score format: "CVSS:3.1/AV:N/..." — the leading "/N.N" base score
			// lives in the metric, but OSV sometimes also returns a bare "7.5".
			// Fall through with a best-effort parse on the leading number.
			score := s.Score
			if idx := strings.Index(score, "/"); idx > 0 {
				score = score[:idx]
			}
			switch {
			case strings.HasPrefix(score, "9"), strings.HasPrefix(score, "10"):
				return "critical"
			case strings.HasPrefix(score, "7"), strings.HasPrefix(score, "8"):
				return "high"
			case strings.HasPrefix(score, "4"), strings.HasPrefix(score, "5"), strings.HasPrefix(score, "6"):
				return "medium"
			case strings.HasPrefix(score, "0"), strings.HasPrefix(score, "1"), strings.HasPrefix(score, "2"), strings.HasPrefix(score, "3"):
				return "low"
			}
		}
	}
	return "medium"
}
