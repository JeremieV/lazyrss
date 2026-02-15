package main
import (
	"fmt"
	"github.com/JohannesKaufmann/html-to-markdown/v2"
)
func main() {
	html := "<img src=\"https://example.com/img.png\" alt=\"Alt Text\">"
	md, _ := htmltomarkdown.ConvertString(html)
	fmt.Printf("MD with Alt: %q\n", md)

	htmlNoAlt := "<img src=\"https://example.com/img.png\">"
	mdNoAlt, _ := htmltomarkdown.ConvertString(htmlNoAlt)
	fmt.Printf("MD no Alt: %q\n", mdNoAlt)
}
