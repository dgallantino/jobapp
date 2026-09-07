package scrape

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const instagramCanonicalHost = "www.instagram.com"

var (
	// /p|reel|tv/CODE or /username/p|reel|tv/CODE
	instagramPostPathRE = regexp.MustCompile(`(?i)^/(?:[^/]+/)?(?:p|reel|tv)/([A-Za-z0-9_-]+)/?$`)

	instagramCaptionTextRE = regexp.MustCompile(`"caption"\s*:\s*\{\s*"text"\s*:\s*"((?:\\.|[^"\\])*)"`)
)

// instagramAdapter parses a single Instagram post URL into one JobAd.
// It is link-only: caption text only, no image OCR, no listing crawl.
type instagramAdapter struct {
	Client    *Client
	Extractor fieldExtractor
}

// newInstagramAdapter returns an Instagram adapter using client (or NewClient defaults if nil).
func newInstagramAdapter(client *Client, extractor fieldExtractor) *instagramAdapter {
	if client == nil {
		client = NewClient(ClientOptions{})
	}
	return &instagramAdapter{Client: client, Extractor: extractor}
}

func (a *instagramAdapter) Name() string { return "instagram" }

// Scrape fetches pageURL as a single Instagram post and returns one JobAd.
func (a *instagramAdapter) Scrape(ctx context.Context, pageURL string) ([]JobAd, error) {
	html, finalURL, fetchErr := a.Client.FetchBytes(ctx, pageURL)
	var (
		ad      JobAd
		caption string
		err     error
	)
	if fetchErr == nil {
		ad, caption, err = parseInstagramPost(string(html), firstNonEmpty(finalURL, pageURL))
	}
	if fetchErr != nil || err != nil {
		rendered, loc, rerr := a.Client.Render(ctx, pageURL, `meta[property="og:description"]`)
		if rerr != nil {
			if fetchErr != nil {
				return nil, fmt.Errorf("instagram: %w", fetchErr)
			}
			return nil, fmt.Errorf("instagram: %w", err)
		}
		ad, caption, err = parseInstagramPost(rendered, firstNonEmpty(loc, pageURL))
		if err != nil {
			return nil, err
		}
	}

	fillEmptyJobFields(ctx, a.Extractor, &ad, caption, "instagram")
	return []JobAd{ad}, nil
}

func parseInstagramPost(html, pageURL string) (JobAd, string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return JobAd{}, "", fmt.Errorf("instagram: parse html: %w", err)
	}

	caption := extractInstagramCaption(html, doc)
	if strings.TrimSpace(caption) == "" {
		return JobAd{}, "", fmt.Errorf("instagram: no post text")
	}

	canonical := canonicalInstagramURL(pageURL, metaContent(doc, "og:url"))
	title, company, salary := extractThreadsFields(unstylizeLetters(caption))
	return JobAd{
		SourceURL:   canonical,
		Title:       title,
		Company:     company,
		Salary:      salary,
		Description: caption,
	}, caption, nil
}

func extractInstagramCaption(html string, doc *goquery.Document) string {
	seed := unwrapInstagramQuoted(metaContent(doc, "og:description"))
	if seed == "" {
		seed = unwrapInstagramQuoted(metaContent(doc, "twitter:description"))
	}
	if seed == "" {
		seed = unwrapInstagramQuoted(metaContent(doc, "og:title"))
	}

	best := seed
	for _, p := range extractInstagramCaptionTexts(html) {
		if !captionMatchesSeed(p, seed) {
			continue
		}
		if len(p) > len(best) {
			best = p
		}
	}
	return strings.TrimSpace(best)
}

// unwrapInstagramQuoted strips Instagram's likes/title wrapper:
// `N likes, M comments - user on Month DD, YYYY: "CAPTION"`
// and `Display Name on Instagram: "CAPTION"`.
func unwrapInstagramQuoted(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const marker = `: "`
	i := strings.Index(s, marker)
	if i < 0 {
		return s
	}
	inner := s[i+len(marker):]
	inner = strings.TrimSpace(inner)
	inner = strings.TrimSuffix(inner, `".`)
	inner = strings.TrimSpace(inner)
	inner = strings.TrimSuffix(inner, `"`)
	return strings.TrimSpace(inner)
}

func extractInstagramCaptionTexts(html string) []string {
	matches := instagramCaptionTextRE.FindAllStringSubmatch(html, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		decoded, err := strconv.Unquote(`"` + m[1] + `"`)
		if err != nil {
			continue
		}
		decoded = strings.TrimSpace(decoded)
		if decoded != "" {
			out = append(out, decoded)
		}
	}
	return out
}

func canonicalInstagramURL(pageURL, ogURL string) string {
	for _, candidate := range []string{ogURL, pageURL} {
		u, err := url.Parse(strings.TrimSpace(candidate))
		if err != nil {
			continue
		}
		if !isInstagramHost(u.Hostname()) {
			continue
		}
		m := instagramPostPathRE.FindStringSubmatch(u.Path)
		if m == nil {
			continue
		}
		return "https://" + instagramCanonicalHost + "/p/" + m[1]
	}
	u, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return strings.TrimSpace(pageURL)
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func isInstagramHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	return host == "instagram.com" || host == "instagr.am"
}

// unstylizeLetters maps Unicode mathematical alphanumerics and fullwidth
// Latin letters/digits to ASCII so hiring/company regexes can match IG captions.
func unstylizeLetters(s string) string {
	return strings.Map(unstylizeRune, s)
}

func unstylizeRune(r rune) rune {
	switch {
	case r >= 0x1D400 && r <= 0x1D6A3:
		idx := (r - 0x1D400) % 52
		if idx < 26 {
			return 'A' + idx
		}
		return 'a' + (idx - 26)
	case r >= 0x1D7CE && r <= 0x1D7FF:
		return '0' + (r-0x1D7CE)%10
	case r >= 0xFF21 && r <= 0xFF3A:
		return 'A' + (r - 0xFF21)
	case r >= 0xFF41 && r <= 0xFF5A:
		return 'a' + (r - 0xFF41)
	case r >= 0xFF10 && r <= 0xFF19:
		return '0' + (r - 0xFF10)
	default:
		return r
	}
}
