package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The OIDC issuer check (#314): before guided configuration hands the
// OIDC_ISSUER answer to the configure step, the launcher fetches the
// provider's discovery document (OpenID Connect Discovery 1.0 §4) and
// compares its issuer with the answer exactly (§4.3), so a dropped or
// extra trailing slash is caught while the person can still fix it. It
// runs here because the configure step has no network (--network none).
// The request follows the bounded-probe shape of health.go, with the
// limits of Orbit's own install-time check (verifyOidcDiscovery, ai/orbit
// src/lib/oidc-discovery.ts): https only on every redirect, a time limit
// and a byte cap.

// The limits, Orbit's own: DISCOVERY_TIMEOUT_MS, OIDC_DISCOVERY_MAX_BYTES
// and MAXIMUM_REDIRECTS. IssuerEchoMaxBytes bounds a published issuer
// the screen may repeat (#314).
const (
	IssuerDiscoveryTimeout  = 10 * time.Second
	IssuerDiscoveryMaxBytes = 1 << 20
	IssuerEchoMaxBytes      = 2048
	issuerMaxRedirects      = 10
)

// IssuerVerdict is how the answer compares with the published issuer.
type IssuerVerdict int

const (
	// IssuerUnchecked: no document was read; Reason says why.
	IssuerUnchecked IssuerVerdict = iota
	IssuerMatches
	// IssuerSlashDiffers: equal but for one trailing slash.
	IssuerSlashDiffers
	IssuerDiffers
)

// IssuerCheckReason says why a check could not be made, in the words the
// screen shows after "could not check this issuer: ".
type IssuerCheckReason string

// The reasons. "no answer within <limit>" and "answered with status
// <code>" are built from these prefixes.
const (
	IssuerUnreachable    IssuerCheckReason = "the address could not be reached"
	IssuerNotFound       IssuerCheckReason = "address not found"
	IssuerCertRejected   IssuerCheckReason = "certificate rejected"
	IssuerNotHTTPS       IssuerCheckReason = "redirected to a non-https address"
	IssuerTooLarge       IssuerCheckReason = "the document is larger than 1 MiB"
	IssuerNotJSON        IssuerCheckReason = "the answer is not a JSON document"
	IssuerNoIssuer       IssuerCheckReason = "the document names no issuer"
	IssuerNotWellShaped  IssuerCheckReason = "not a plain https:// address"
	issuerNoAnswerWithin                   = "no answer within "
	issuerAnsweredStatus                   = "answered with status "
)

// IssuerCheck is one check's result. Published is the provider's issuer
// when it differs and may be shown (EchoableIssuer); Withheld is true
// when it differs and may not. Reason is set only when Unchecked.
type IssuerCheck struct {
	Verdict   IssuerVerdict
	Published string
	Withheld  bool
	Reason    IssuerCheckReason
}

var (
	errIssuerNotHTTPS      = errors.New("redirected to a non-https address")
	errIssuerManyRedirects = errors.New("too many redirects")
)

// issuerRedirect follows at most issuerMaxRedirects redirects, each to
// an https address (curl --proto-redir '=https' --max-redirs in Orbit's
// original install.sh).
func issuerRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return errIssuerNotHTTPS
	}
	if len(via) > issuerMaxRedirects {
		return errIssuerManyRedirects
	}
	return nil
}

// issuerClient is the check's client when the caller gives none;
// http.DefaultClient has no time limit.
var issuerClient = &http.Client{Timeout: IssuerDiscoveryTimeout, CheckRedirect: issuerRedirect}

// WellShapedIssuer is Orbit's isValidOidcIssuer shape (ai/orbit
// src/lib/config-contract.ts) less its host rules, which stay the
// engine's: https://, a non-empty authority, no whitespace or control
// characters, no credentials, query or fragment. Only a well-shaped
// answer is ever requested.
func WellShapedIssuer(s string) bool {
	rest, ok := strings.CutPrefix(s, "https://")
	if !ok || strings.ContainsAny(s, "@?#") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	authority, _, _ := strings.Cut(rest, "/")
	return authority != ""
}

// EchoableIssuer reports whether a published issuer may be drawn on the
// operator's terminal: well-shaped, printable ASCII only (0x21-0x7E, so
// no escape sequence or bidi override) and at most IssuerEchoMaxBytes
// (owner, 2026-10-30).
func EchoableIssuer(s string) bool {
	if len(s) > IssuerEchoMaxBytes || !WellShapedIssuer(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// DiscoveryURL is the issuer's discovery document address, joined with
// exactly one slash (Orbit's buildDiscoveryUrl).
func DiscoveryURL(issuer string) string {
	if strings.HasSuffix(issuer, "/") {
		return issuer + ".well-known/openid-configuration"
	}
	return issuer + "/.well-known/openid-configuration"
}

// CheckOidcIssuer fetches answer's discovery document and compares the
// issuer it names with answer. client carries the request (nil is the
// launcher's own); its time limit and redirect rule are replaced on a
// copy, never on the caller's. The check gives up after
// IssuerDiscoveryTimeout, or sooner if ctx ends. An answer that is not
// WellShapedIssuer is never requested.
func CheckOidcIssuer(ctx context.Context, client *http.Client, answer string) IssuerCheck {
	if !WellShapedIssuer(answer) {
		return IssuerCheck{Reason: IssuerNotWellShaped}
	}
	c := *issuerClient
	if client != nil {
		c = *client
	}
	c.Timeout, c.CheckRedirect = IssuerDiscoveryTimeout, issuerRedirect

	limit := limitApplied(ctx, IssuerDiscoveryTimeout)
	ctx, cancel := context.WithTimeout(ctx, IssuerDiscoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DiscoveryURL(answer), nil)
	if err != nil {
		return IssuerCheck{Reason: IssuerUnreachable}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return IssuerCheck{Reason: issuerFailure(ctx, err, limit)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return IssuerCheck{Reason: IssuerCheckReason(issuerAnsweredStatus + strconv.Itoa(resp.StatusCode))}
	}
	if resp.ContentLength > IssuerDiscoveryMaxBytes {
		return IssuerCheck{Reason: IssuerTooLarge}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, IssuerDiscoveryMaxBytes+1))
	if err != nil {
		return IssuerCheck{Reason: issuerFailure(ctx, err, limit)}
	}
	if len(body) > IssuerDiscoveryMaxBytes {
		return IssuerCheck{Reason: IssuerTooLarge}
	}
	return compareIssuer(answer, body)
}

// compareIssuer reads the document's issuer and compares it with answer
// exactly (Discovery §4.3).
func compareIssuer(answer string, body []byte) IssuerCheck {
	if !json.Valid(body) {
		return IssuerCheck{Reason: IssuerNotJSON}
	}
	// A map, not a struct: encoding/json matches struct fields whatever
	// their case, and only the exact key "issuer" counts (Discovery §3).
	var doc map[string]json.RawMessage
	var published string
	issuer := []byte(nil)
	if json.Unmarshal(body, &doc) == nil {
		issuer = doc["issuer"]
	}
	if len(issuer) == 0 || issuer[0] != '"' || json.Unmarshal(issuer, &published) != nil {
		return IssuerCheck{Reason: IssuerNoIssuer}
	}
	verdict := IssuerDiffers
	switch {
	case published == answer:
		return IssuerCheck{Verdict: IssuerMatches}
	// One trailing slash, on one side only: "x/" against "x//" is no
	// simple slash difference, and the words for one would mislead.
	case answer+"/" == published && !strings.HasSuffix(answer, "/"),
		published+"/" == answer && !strings.HasSuffix(published, "/"):
		verdict = IssuerSlashDiffers
	}
	if !EchoableIssuer(published) {
		return IssuerCheck{Verdict: verdict, Withheld: true}
	}
	return IssuerCheck{Verdict: verdict, Published: published}
}

// issuerFailure names a failed request's cause the way transportReason
// does for the health probe; limit is the time the check was given. Too
// many redirects reads as unreachable, as Orbit classifies it (curl's
// exit 47 is "provider unavailable").
func issuerFailure(ctx context.Context, err error, limit time.Duration) IssuerCheckReason {
	var netErr net.Error
	switch {
	case errors.Is(err, errIssuerNotHTTPS):
		return IssuerNotHTTPS
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return IssuerCheckReason(issuerNoAnswerWithin + limit.String())
	}
	switch transportReason(err) {
	case HealthAddressNotFound:
		return IssuerNotFound
	case HealthCertificateRejected:
		return IssuerCertRejected
	}
	return IssuerUnreachable
}
