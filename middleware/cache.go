package middleware

import (
	"path"
	"regexp"

	"github.com/gin-gonic/gin"
)

// contentHashedAsset matches frontend build file names that embed a content
// hash, for example index.0f1e757955.js or vendor-ui.1a2b3c4d.css. The URL
// changes whenever the content changes, so the response can be cached forever.
var contentHashedAsset = regexp.MustCompile(`[.-][0-9a-f]{8,}\.[a-z0-9]+$`)

func Cache() func(c *gin.Context) {
	return func(c *gin.Context) {
		switch {
		case c.Request.RequestURI == "/":
			c.Header("Cache-Control", "no-cache")
		case contentHashedAsset.MatchString(path.Base(c.Request.URL.Path)):
			const immutable = "public, max-age=31536000, immutable"
			c.Header("Cache-Control", immutable)
			// Cloudflare reads CDN-Cache-Control before Cache-Control, so the
			// edge keeps hashed assets for a year independently of browser TTL
			// settings.
			c.Header("CDN-Cache-Control", immutable)
		default:
			c.Header("Cache-Control", "max-age=604800") // one week
		}
		c.Header("Cache-Version", "b688f2fb5be447c25e5aa3bd063087a83db32a288bf6a4f35f2d8db310e40b14")
		c.Next()
	}
}
