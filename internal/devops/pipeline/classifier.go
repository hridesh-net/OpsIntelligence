package pipeline

import (
	"path/filepath"
	"strings"
)

// ComplexityLevel determines which LLM tier handles a review.
type ComplexityLevel int

const (
	// ComplexityLow — small diff with no sensitive files.
	// Routes to local intel (Gemma) which has no rate limits.
	ComplexityLow ComplexityLevel = iota

	// ComplexityHigh — large diff or security-sensitive files.
	// Routes to the primary (remote) LLM provider.
	ComplexityHigh
)

// ComplexityClassifier inspects a PR diff and decides the routing tier.
type ComplexityClassifier struct {
	// LocalIntelDiffMax is the maximum number of changed lines that still
	// qualifies as ComplexityLow (default 200). Set to 0 to always use remote.
	LocalIntelDiffMax int
}

// NewComplexityClassifier returns a classifier with sensible defaults.
func NewComplexityClassifier(localIntelDiffMax int) *ComplexityClassifier {
	if localIntelDiffMax <= 0 {
		localIntelDiffMax = 200
	}
	return &ComplexityClassifier{LocalIntelDiffMax: localIntelDiffMax}
}

// Classify returns the complexity level for a PR given its diff size and file paths.
func (c *ComplexityClassifier) Classify(diffLines int, filePaths []string) ComplexityLevel {
	if diffLines > c.LocalIntelDiffMax {
		return ComplexityHigh
	}
	for _, p := range filePaths {
		if isSensitivePath(p) {
			return ComplexityHigh
		}
	}
	return ComplexityLow
}

// isSensitivePath returns true when a file path touches security-sensitive
// areas that require the full capability of the primary remote LLM.
func isSensitivePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))

	// Directory segments that indicate security-sensitive code.
	sensitiveSegments := []string{
		"auth", "authn", "authz", "oauth", "oidc", "saml",
		"crypto", "encrypt", "decrypt", "cipher", "tls", "ssl", "cert",
		"secret", "secrets", "credentials", "creds",
		"password", "passwd", "token", "jwt", "api_key", "apikey",
		"permission", "permissions", "acl", "rbac", "policy",
		"firewall", "iptables", "selinux", "seccomp",
		"sandbox", "escape", "injection",
		"sql", "migration", "schema", // schema changes can have wide security impact
		"iam", "roles", "grants",
	}
	for _, seg := range sensitiveSegments {
		if strings.Contains(lower, "/"+seg+"/") ||
			strings.HasPrefix(lower, seg+"/") {
			return true
		}
	}

	// File base names that indicate security-sensitive content.
	sensitiveFiles := []string{
		"auth.go", "auth.py", "auth.js", "auth.ts",
		"middleware.go", "middleware.py", "middleware.js",
		"jwt.go", "jwt.py", "jwt.js",
		"crypto.go", "crypto.py", "crypto.js",
		"rbac.go", "rbac.py",
		"permissions.go", "permissions.py",
		"secrets.go", "secrets.py",
		"security.go", "security.py", "security.js",
		".env", ".env.example", ".envrc",
		"dockerfile", "docker-compose.yml", "docker-compose.yaml",
		"makefile",                       // often contains secret injection patterns
		"terraform.tf", "main.tf",        // infrastructure-as-code
		"helm", "values.yaml",            // k8s secrets
		"kubeconfig", "kustomization.yaml",
	}
	for _, f := range sensitiveFiles {
		if base == f {
			return true
		}
	}

	// Extension-level heuristics.
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pem", ".key", ".crt", ".pfx", ".p12", ".cer":
		return true
	}

	return false
}
