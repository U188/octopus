package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	maxDatabaseImportBytes int64 = 256 << 20
	maxSiteImportBytes     int64 = 32 << 20
	multipartOverheadBytes int64 = 1 << 20
)

var errUploadTooLarge = errors.New("upload exceeds size limit")

func limitRequestBody(c *gin.Context, maxBytes int64, multipart bool) {
	limit := maxBytes
	if multipart {
		limit += multipartOverheadBytes
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
}

func readUploadPayload(r io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, fmt.Errorf("%w: maximum is %d bytes", errUploadTooLarge, maxBytes)
		}
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: maximum is %d bytes", errUploadTooLarge, maxBytes)
	}
	return data, nil
}
