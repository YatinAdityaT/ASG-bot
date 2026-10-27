package messageapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

func bearerTokenMatches(request *http.Request, expected string) bool {
	authorization := request.Header.Get("Authorization")
	presented := ""
	validFormat := 0
	if strings.HasPrefix(authorization, bearerPrefix) {
		presented = strings.TrimPrefix(authorization, bearerPrefix)
		if presented != "" {
			validFormat = 1
		}
	}

	presentedDigest := sha256.Sum256([]byte(presented))
	expectedDigest := sha256.Sum256([]byte(expected))
	return validFormat&subtle.ConstantTimeCompare(presentedDigest[:], expectedDigest[:]) == 1
}
