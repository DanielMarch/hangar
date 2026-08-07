// Package transport builds the http.RoundTripper chain every outbound ESI
// request goes through: user agent, the app-pinned X-Compatibility-Date
// header, and bounded retry of transient failures. Rate limiting and
// circuit breaking (Governor 1/2, the consumption ledger) are Phase 4 —
// this package has no opinion on 429s or the error budget; it only decides
// whether a *transport-level* failure or 5xx is worth one more attempt.
package transport

import "fmt"

// BuildUserAgent renders ESI's required descriptive user agent: an app
// identifier, a version, and contact information CCP (or any operator
// investigating abusive traffic) can use to reach whoever runs this
// specific installation. HANGAR is self-hosted per deployment, so
// contactURL should be the installation's own public URL or an operator
// email — never a hangar-project address, which couldn't help anyone
// reach the operator actually making these requests.
func BuildUserAgent(version, contactURL string) string {
	if version == "" {
		version = "dev"
	}
	if contactURL == "" {
		contactURL = "no-contact-configured"
	}
	return fmt.Sprintf("HANGAR/%s (+%s)", version, contactURL)
}
