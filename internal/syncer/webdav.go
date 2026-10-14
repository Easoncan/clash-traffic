package syncer

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"clashtraffic/internal/store"
)

// WebDAV keeps the devices' directories under Dir of a WebDAV server, such
// as Nutstore (坚果云), Nextcloud or a NAS.
type WebDAV struct {
	URL      string // e.g. https://dav.jianguoyun.com/dav/
	Username string
	Password string
	Dir      string // directory under URL, e.g. ClashTraffic

	hc   *http.Client
	made map[string]bool // collections known to exist
}

// NewWebDAV returns a WebDAV backend.
func NewWebDAV(rawURL, user, pass, dir string) *WebDAV {
	if dir == "" {
		dir = "ClashTraffic"
	}
	return &WebDAV{
		URL: strings.TrimRight(rawURL, "/") + "/", Username: user, Password: pass, Dir: strings.Trim(dir, "/"),
		hc: &http.Client{Timeout: 60 * time.Second}, made: map[string]bool{},
	}
}

func (w *WebDAV) ID() string { return "webdav:" + w.URL + w.Dir + "@" + w.Username }

func (w *WebDAV) url(parts ...string) string {
	u := w.URL + url.PathEscape(w.Dir) + "/"
	for i, p := range parts {
		u += url.PathEscape(p)
		if i < len(parts)-1 {
			u += "/"
		}
	}
	return u
}

func (w *WebDAV) req(ctx context.Context, method, u string, body []byte, header map[string]string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if w.Username != "" || w.Password != "" {
		req.SetBasicAuth(w.Username, w.Password)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := w.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, fmt.Errorf("认证失败（%s），请检查用户名和密码（坚果云需使用“应用密码”）", resp.Status)
	}
	return resp, nil
}

func (w *WebDAV) mkcol(ctx context.Context, u string) error {
	if w.made[u] {
		return nil
	}
	resp, err := w.req(ctx, "MKCOL", u, nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// 405: it exists already.
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("创建目录 %s: %s", u, resp.Status)
	}
	w.made[u] = true
	return nil
}

func (w *WebDAV) Put(ctx context.Context, device, name string, data []byte) (string, error) {
	if err := w.mkcol(ctx, w.url()); err != nil {
		return "", err
	}
	if err := w.mkcol(ctx, w.url(device)+"/"); err != nil {
		return "", err
	}
	u := w.url(device, name)
	put := func() (int, string, error) {
		resp, err := w.req(ctx, http.MethodPut, u, data, map[string]string{"Content-Type": "application/octet-stream"})
		if err != nil {
			return 0, "", err
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Status, nil
	}
	code, status, err := put()
	if err != nil {
		return "", err
	}
	if code == http.StatusConflict {
		// The directory went away since it was made: make it again.
		w.made = map[string]bool{}
		if err := w.mkcol(ctx, w.url()); err != nil {
			return "", err
		}
		if err := w.mkcol(ctx, w.url(device)+"/"); err != nil {
			return "", err
		}
		if code, status, err = put(); err != nil {
			return "", err
		}
	}
	if code/100 != 2 {
		return "", fmt.Errorf("PUT %s: %s", name, status)
	}
	// The tag a listing will show for the file just written. Unknown, the
	// next listing's is taken for it.
	entries, err := w.propfind(ctx, u, "0")
	if err != nil || len(entries) != 1 {
		return "", nil
	}
	return entries[0].tag, nil
}

func (w *WebDAV) Get(ctx context.Context, device, name string) ([]byte, error) {
	resp, err := w.req(ctx, http.MethodGet, w.url(device, name), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", name, resp.Status)
	}
	return store.ReadLimited(resp.Body, store.MaxFileBytes)
}

func (w *WebDAV) Delete(ctx context.Context, device, name string) error {
	resp, err := w.req(ctx, http.MethodDelete, w.url(device, name), nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

type multistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Status string `xml:"status"`
			Prop   struct {
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Length   string `xml:"getcontentlength"`
				ETag     string `xml:"getetag"`
				Modified string `xml:"getlastmodified"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

type entry struct {
	path  string
	name  string
	isDir bool
	tag   string
}

const propfind = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/><d:getetag/><d:getlastmodified/></d:prop></d:propfind>`

// list returns the entries of a collection, without the collection itself.
func (w *WebDAV) list(ctx context.Context, u string) ([]entry, error) {
	all, err := w.propfind(ctx, u, "1")
	if err != nil {
		return nil, err
	}
	self, _ := url.Parse(u)
	selfPath := strings.TrimRight(self.Path, "/")
	var out []entry
	for _, e := range all {
		if strings.TrimRight(e.path, "/") != selfPath {
			out = append(out, e)
		}
	}
	return out, nil
}

// propfind returns the entries PROPFIND reports at u, to depth.
func (w *WebDAV) propfind(ctx context.Context, u, depth string) ([]entry, error) {
	resp, err := w.req(ctx, "PROPFIND", u, []byte(propfind), map[string]string{"Depth": depth, "Content-Type": "application/xml; charset=utf-8"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("PROPFIND %s: %s", u, resp.Status)
	}
	var ms multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, err
	}
	var out []entry
	for _, r := range ms.Responses {
		h, err := url.Parse(r.Href)
		if err != nil {
			continue
		}
		e := entry{path: h.Path, name: path.Base(strings.TrimRight(h.Path, "/"))}
		for _, ps := range r.Propstat {
			if ps.Status != "" && !strings.Contains(ps.Status, " 200") {
				continue
			}
			pr := ps.Prop
			if pr.ResourceType.Collection != nil {
				e.isDir = true
			}
			e.tag = strings.Trim(pr.ETag, `"`)
			if e.tag == "" {
				e.tag = pr.Length + ":" + pr.Modified
			}
		}
		if strings.HasSuffix(r.Href, "/") {
			e.isDir = true
		}
		out = append(out, e)
	}
	return out, nil
}

func (w *WebDAV) List(ctx context.Context) ([]RemoteFile, error) {
	devs, err := w.list(ctx, w.url())
	if err != nil {
		return nil, err
	}
	var out []RemoteFile
	for _, d := range devs {
		if !d.isDir || !store.ValidDeviceID(d.name) {
			continue
		}
		files, err := w.list(ctx, w.url(d.name)+"/")
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if !f.isDir {
				out = append(out, RemoteFile{Device: d.name, Name: f.name, Tag: f.tag})
			}
		}
	}
	return out, nil
}
