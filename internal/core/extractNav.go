package core

import (
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var patternsNext = []string{
	"next",
	"→",
	">",
	">>",
	"next chapter",
	"continue reading",
	"continue",
	"read more",
	"next page",
	"forward",
	"next post",
	"next article",
	"next entry",
	"next story",
	"next part",
	"next section",
	"proceed",
	"advance",
	"onward",
	"siguiente",
	"weiter",
	"suivant",
}

var patternsPrev = []string{
	"previous",
	"prev",
	"←",
	"<",
	"<<",
	"back",
	"previous chapter",
	"prev chapter",
	"previous page",
	"back to",
	"previous post",
	"prev post",
	"previous article",
	"prev article",
	"previous entry",
	"prev entry",
	"previous story",
	"prev story",
	"previous part",
	"prev part",
	"previous section",
	"prev section",
	"return",
	"go back",
	"anterior",
	"zurück",
	"précédent",
}

const selector = `
	a[href],
	button[onclick],
	button[data-href],
	button[data-url],
	button[data-link],
	nav a,
	nav button,
	nav span,
	nav div,
	div[onclick],
	div[data-href],
	div[data-url],
	div[data-link],
	span[onclick],
	span[data-href],
	span[data-url],
	span[data-link],
	[role='button'],
	[role='link'],
	.btn,
	.button,
	.nav-link,
	.navigation,
	.pager,
	.pagination,
	.next,
	.prev,
	.previous,
	.continue,
	.forward,
	.back,
	li a,
	li button,
	li span,
	li div,
	.page-numbers,
	.page-link,
	.wp-pagenavi,
	input[type='button'],
	input[type='submit']
`

type ScoredLink struct {
	URL     string
	Score   int
	Element *goquery.Selection
}

func isURLsameSiteDiffPage(pageURL string, elemURL string) bool {
	baseU, err := url.Parse(pageURL)
	if err != nil {
		return false
	}

	elemU, err := baseU.Parse(elemURL)
	if err != nil {
		return false
	}

	return elemU.Host == baseU.Host && elemU.Path != baseU.Path
}

func getURLfromElem(s *goquery.Selection) string {
	if href := s.AttrOr("href", ""); href != "" {
		return href
	}
	if dataHref := s.AttrOr("data-href", ""); dataHref != "" {
		return dataHref
	}
	if dataUrl := s.AttrOr("data-url", ""); dataUrl != "" {
		return dataUrl
	}
	if dataLink := s.AttrOr("data-link", ""); dataLink != "" {
		return dataLink
	}
	if onclick := s.AttrOr("onclick", ""); onclick != "" {
		return extractURLFromOnclick(onclick)
	}
	return ""
}

func extractURLFromOnclick(onclick string) string {
	onclick = strings.TrimSpace(onclick)
	if strings.Contains(onclick, "location.href") {
		start := strings.Index(onclick, "'")
		if start == -1 {
			start = strings.Index(onclick, "\"")
		}
		if start != -1 {
			end := strings.Index(onclick[start+1:], onclick[start:start+1])
			if end != -1 {
				return onclick[start+1 : start+1+end]
			}
		}
	}
	if strings.Contains(onclick, "window.location") {
		start := strings.Index(onclick, "'")
		if start == -1 {
			start = strings.Index(onclick, "\"")
		}
		if start != -1 {
			end := strings.Index(onclick[start+1:], onclick[start:start+1])
			if end != -1 {
				return onclick[start+1 : start+1+end]
			}
		}
	}
	return ""
}

func matchesPatterns(s *goquery.Selection, patterns []string) bool {
	text := strings.ToLower(strings.TrimSpace(s.Text()))
	id := strings.ToLower(s.AttrOr("id", ""))
	class := strings.ToLower(s.AttrOr("class", ""))
	title := strings.ToLower(s.AttrOr("title", ""))
	ariaLabel := strings.ToLower(s.AttrOr("aria-label", ""))
	alt := strings.ToLower(s.AttrOr("alt", ""))

	searchFields := []string{text, id, class, title, ariaLabel, alt}

	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		for _, field := range searchFields {
			if field == pattern || strings.Contains(field, pattern) {
				return true
			}
		}
	}
	return false
}

func resolveURL(elemURL string, baseURL string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return elemURL
	}

	resolved, err := base.Parse(elemURL)
	if err != nil {
		return elemURL
	}

	return resolved.String()
}

func scoreElement(s *goquery.Selection, patterns []string) int {
	score := 0
	// debugInfo := []string{}

	// Tier 1: Semantic Attributes (Highest Priority)
	rel := strings.ToLower(s.AttrOr("rel", ""))
	if rel == "next" || rel == "prev" {
		score += 1000
		// debugInfo = append(debugInfo, fmt.Sprintf("rel=%s (+1000)", rel))
	}

	// Tier 2: Navigation Context (High Priority)
	// Check if element is inside nav tag
	if s.Closest("nav").Length() > 0 {
		score += 500
		// debugInfo = append(debugInfo, "inside nav (+500)")
	}

	// Check for navigation classes
	class := strings.ToLower(s.AttrOr("class", ""))
	navClasses := []string{"nav-next", "nav-previous", "navigation", "pager", "pagination"}
	for _, navClass := range navClasses {
		if strings.Contains(class, navClass) {
			score += 300
			// debugInfo = append(debugInfo, fmt.Sprintf("nav class %s (+300)", navClass))
			break
		}
	}

	// Tier 3: Text Match Quality (Medium Priority)
	// Calculate pattern-to-text ratio for main text
	text := strings.ToLower(strings.TrimSpace(s.Text()))
	if text != "" {
		bestRatio := 0.0
		// matchedPattern := ""
		for _, pattern := range patterns {
			pattern = strings.ToLower(strings.TrimSpace(pattern))
			if strings.Contains(text, pattern) {
				ratio := float64(len(pattern)) / float64(len(text))
				if ratio > bestRatio {
					bestRatio = ratio
					// matchedPattern = pattern
				}
			}
		}
		if bestRatio > 0 {
			textScore := int(100 * bestRatio)
			score += textScore
			// debugInfo = append(debugInfo, fmt.Sprintf("text ratio %s in '%s' (+%d)", matchedPattern, text, textScore))
		}
	}

	// Also check other attributes for text matches (lower weight)
	searchFields := []string{
		strings.ToLower(s.AttrOr("id", "")),
		class,
		strings.ToLower(s.AttrOr("title", "")),
		strings.ToLower(s.AttrOr("aria-label", "")),
		strings.ToLower(s.AttrOr("alt", "")),
	}

	for _, field := range searchFields {
		if field != "" {
			bestRatio := 0.0
			for _, pattern := range patterns {
				pattern = strings.ToLower(strings.TrimSpace(pattern))
				if strings.Contains(field, pattern) {
					ratio := float64(len(pattern)) / float64(len(field))
					if ratio > bestRatio {
						bestRatio = ratio
					}
				}
			}
			if bestRatio > 0 {
				attrScore := int(50 * bestRatio)
				score += attrScore
				// debugInfo = append(debugInfo, fmt.Sprintf("attr match (+%d)", attrScore))
			}
		}
	}

	// Tier 4: Content Filtering (Bonus/Penalty)
	// Check for sidebar/widget areas
	sidebarSelectors := []string{"#sidebar", "#secondary", ".widget-area", ".sidebar"}
	for _, selector := range sidebarSelectors {
		if s.Closest(selector).Length() > 0 {
			score -= 200
			// debugInfo = append(debugInfo, fmt.Sprintf("in sidebar %s (-200)", selector))
			break
		}
	}

	// Check if inside a large list (many similar links)
	parentList := s.Closest("ul, ol")
	if parentList.Length() > 0 {
		linkCount := parentList.Find("a").Length()
		if linkCount > 10 { // Arbitrary threshold for "many links"
			score -= 100
			// debugInfo = append(debugInfo, fmt.Sprintf("large list %d links (-100)", linkCount))
		}
	}

	// Debug logging
	// href := s.AttrOr("href", "")
	// if href != "" {
	// 	log.Printf("SCORE: %d for %s | %s | %v", score, href, strings.Join(debugInfo, ", "), text)
	// }

	return score
}

func hasSemanticNavAttributes(s *goquery.Selection, direction string) bool {
	rel := strings.ToLower(s.AttrOr("rel", ""))
	if direction == "next" && rel == "next" {
		return true
	}
	if direction == "prev" && rel == "prev" {
		return true
	}
	return false
}

type Nav struct {
	Next string
	Prev string
}

func extractNav(htmlContent string, baseURL string) *Nav {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return &Nav{}
	}

	var nextCandidates []ScoredLink
	var prevCandidates []ScoredLink

	doc.Find(selector).Each(func(i int, s *goquery.Selection) {
		elemURL := getURLfromElem(s)
		if elemURL == "" || !isURLsameSiteDiffPage(baseURL, elemURL) {
			return
		}

		resolvedURL := resolveURL(elemURL, baseURL)

		// Check if it matches next patterns OR has semantic next attributes
		if matchesPatterns(s, patternsNext) || hasSemanticNavAttributes(s, "next") {
			score := scoreElement(s, patternsNext)
			nextCandidates = append(nextCandidates, ScoredLink{
				URL:     resolvedURL,
				Score:   score,
				Element: s,
			})
		}

		// Check if it matches prev patterns OR has semantic prev attributes
		if matchesPatterns(s, patternsPrev) || hasSemanticNavAttributes(s, "prev") {
			score := scoreElement(s, patternsPrev)
			prevCandidates = append(prevCandidates, ScoredLink{
				URL:     resolvedURL,
				Score:   score,
				Element: s,
			})
		}
	})

	result := &Nav{}

	// Find highest scoring next link
	if len(nextCandidates) > 0 {
		bestNext := nextCandidates[0]
		for _, candidate := range nextCandidates[1:] {
			if candidate.Score > bestNext.Score {
				bestNext = candidate
			}
		}
		result.Next = bestNext.URL
		// log.Printf("SELECTED NEXT: %s (score: %d)", bestNext.URL, bestNext.Score)
	}

	// Find highest scoring prev link
	if len(prevCandidates) > 0 {
		bestPrev := prevCandidates[0]
		for _, candidate := range prevCandidates[1:] {
			if candidate.Score > bestPrev.Score {
				bestPrev = candidate
			}
		}
		result.Prev = bestPrev.URL
		// log.Printf("SELECTED PREV: %s (score: %d)", bestPrev.URL, bestPrev.Score)
	}

	return result
}
