package orrery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// maxIconSize is the largest custom icon the hub takes, in bytes.
const maxIconSize = 256 << 10

// ErrIconNotFound is the hub's answer for a name that is neither a custom
// icon nor a built-in brand file.
var ErrIconNotFound = errors.New("orrery: icon not found")

var iconName = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,62}$`)

// iconTypes are the image types the hub takes, by file extension.
var iconTypes = map[string]string{".svg": "image/svg+xml", ".png": "image/png", ".webp": "image/webp"}

// iconType is the content type of an icon file, from its extension.
func iconType(file string) (string, error) {
	t, ok := iconTypes[strings.ToLower(path.Ext(file))]
	if !ok {
		return "", fmt.Errorf("icon file %q is not .svg, .png, or .webp", file)
	}
	return t, nil
}

// Image is an icon as the hub serves it.
type Image struct {
	Type string
	Data []byte
	// ETag is the hub's tag for these bytes; pass it back to Icon to
	// revalidate.
	ETag string
	// NotModified is true when the hub answered that the etag passed in is
	// still current. Type and Data are then empty.
	NotModified bool
}

// PutIcon creates or replaces the custom icon name with data of the given
// content type (image/svg+xml, image/png, or image/webp). The token must
// be one that may write entities.
func (c *Client) PutIcon(ctx context.Context, name, contentType string, data []byte) error {
	_, err := c.send(ctx, request{
		method:      http.MethodPut,
		path:        "/v1/icons/" + url.PathEscape(name),
		accept:      "application/json",
		contentType: contentType,
		body:        data,
	})
	return err
}

// Theme is the theme a picture is drawn for. A brand mark drawn in black
// has a copy in each theme's ink; every other icon is the same in both.
type Theme string

const (
	// ThemeDark is the hub's default copy: a black mark in light ink.
	ThemeDark Theme = ""
	// ThemeLight keeps a black mark dark, for a light background.
	ThemeLight Theme = "light"
)

// Icon reads an icon for the dark theme: IconFor with ThemeDark.
func (c *Client) Icon(ctx context.Context, name, etag string) (Image, error) {
	return c.IconFor(ctx, name, ThemeDark, etag)
}

// IconFor reads an icon drawn for theme: the custom icon name, else the
// built-in brand file of that name. With etag set, an unchanged icon
// answers NotModified. A name the hub has no file for is ErrIconNotFound.
func (c *Client) IconFor(ctx context.Context, name string, theme Theme, etag string) (Image, error) {
	path := "/v1/icons/" + url.PathEscape(name)
	if theme != ThemeDark {
		path += "?theme=" + url.QueryEscape(string(theme))
	}
	req := request{method: http.MethodGet, path: path, accept: "image/*"}
	if etag != "" {
		req.header = http.Header{"If-None-Match": {etag}}
	}
	res, err := c.send(ctx, req)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return Image{}, fmt.Errorf("icon %s: %w", name, ErrIconNotFound)
	}
	if err != nil {
		return Image{}, fmt.Errorf("icon %s: %w", name, err)
	}
	if res.status == http.StatusNotModified {
		tag := res.header.Get("ETag")
		if tag == "" {
			tag = etag
		}
		return Image{ETag: tag, NotModified: true}, nil
	}
	return Image{Type: res.header.Get("Content-Type"), Data: res.body, ETag: res.header.Get("ETag")}, nil
}
