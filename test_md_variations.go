package main
import (
	"fmt"
	"regexp"
)
func main() {
	re := regexp.MustCompile(`(!)?\[([^\]]*)\]\(([^\)]+)\)`)
	testCases := []string{
		"![Alt Text](https://example.com/img.png)",
		"![](https://example.com/img.png)",
		"[Link](https://example.com)",
		"![Alt](https://example.com/img.png \"title\")",
	}
	for _, tc := range testCases {
		submatch := re.FindStringSubmatch(tc)
		fmt.Printf("Input: %s, Matches: %v, Len: %d\n", tc, submatch != nil, len(submatch))
	}
}
