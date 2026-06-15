// Package sdss is the library behind the sdss command line:
// the HTTP client, request shaping, and the typed data models for the
// Sloan Digital Sky Survey (SDSS DR18).
//
// The Client sets a real User-Agent, paces requests so a busy session
// stays polite, and retries transient failures (429 and 5xx). Build your
// SQL and catalog searches on top of it.
package sdss

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultUserAgent identifies the client to SDSS.
const DefaultUserAgent = "sdss-cli/dev (+https://github.com/tamnd/sdss-cli)"

// Host is the site this client talks to.
const Host = "skyserver.sdss.org"

// baseURL is the root every request is built from.
const baseURL = "https://" + Host + "/dr18"

// sqlSearchPath is the endpoint for SQL queries.
const sqlSearchPath = "/SkyServerWS/SearchTools/SqlSearch"

// Client talks to the SDSS SkyServer over HTTP.
type Client struct {
	baseURL   string
	HTTP      *http.Client
	UserAgent string
	// Rate is the minimum gap between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int

	last time.Time
}

// NewClient returns a Client with sensible defaults: a 30s timeout,
// a 500ms minimum gap between requests (SDSS can be slow), and 3 retries.
func NewClient() *Client {
	return &Client{
		baseURL:   baseURL,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: DefaultUserAgent,
		Rate:      500 * time.Millisecond,
		Retries:   3,
	}
}

// Get fetches the given URL and returns the response body. It paces and retries
// according to the client's settings.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.Rate <= 0 {
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- wire types ---

type wireTableResult struct {
	TableName string            `json:"TableName"`
	Rows      []json.RawMessage `json:"Rows"`
}

type wirePhotoObj struct {
	ObjID int64   `json:"objid"`
	RA    float64 `json:"ra"`
	Dec   float64 `json:"dec"`
	Type  int     `json:"type"`
	R     float64 `json:"r"`
	G     float64 `json:"g"`
	U     float64 `json:"u"`
	I     float64 `json:"i"`
	Z     float64 `json:"z"`
}

type wireSpecObj struct {
	SpecObjID   int64   `json:"specobjid"`
	RA          float64 `json:"ra"`
	Dec         float64 `json:"dec"`
	Redshift    float64 `json:"z"`
	RedshiftErr float64 `json:"zErr"`
	Class       string  `json:"class"`
	SubClass    string  `json:"subClass"`
}

// --- public output types ---

// PhotoObject is a photometrically detected object in the SDSS catalog.
type PhotoObject struct {
	ID   string  `json:"id"           kit:"id"`
	RA   float64 `json:"ra"`
	Dec  float64 `json:"dec"`
	Type string  `json:"type"`
	MagR float64 `json:"mag_r,omitempty"`
	MagG float64 `json:"mag_g,omitempty"`
	MagU float64 `json:"mag_u,omitempty"`
	MagI float64 `json:"mag_i,omitempty"`
	MagZ float64 `json:"mag_z,omitempty"`
}

// Spectrum is a spectroscopically observed object in the SDSS catalog.
type Spectrum struct {
	ID       string  `json:"id"               kit:"id"`
	RA       float64 `json:"ra"`
	Dec      float64 `json:"dec"`
	Redshift float64 `json:"redshift,omitempty"`
	Class    string  `json:"class,omitempty"`
	SubClass string  `json:"subclass,omitempty"`
}

// photoType converts the SDSS integer type into a human-readable string.
func photoType(t int) string {
	switch t {
	case 3:
		return "galaxy"
	case 6:
		return "star"
	default:
		return strconv.Itoa(t)
	}
}

func toPhotoObject(w wirePhotoObj) *PhotoObject {
	return &PhotoObject{
		ID:   strconv.FormatInt(w.ObjID, 10),
		RA:   w.RA,
		Dec:  w.Dec,
		Type: photoType(w.Type),
		MagR: w.R,
		MagG: w.G,
		MagU: w.U,
		MagI: w.I,
		MagZ: w.Z,
	}
}

func toSpectrum(w wireSpecObj) *Spectrum {
	return &Spectrum{
		ID:       strconv.FormatInt(w.SpecObjID, 10),
		RA:       w.RA,
		Dec:      w.Dec,
		Redshift: w.Redshift,
		Class:    strings.TrimSpace(w.Class),
		SubClass: strings.TrimSpace(w.SubClass),
	}
}

// --- client methods ---

// QuerySQL runs an arbitrary SQL query against the SDSS SkyServer and returns
// the rows from the first table (Table1) as raw JSON maps. The caller is
// responsible for unmarshalling each row into its own type.
func (c *Client) QuerySQL(ctx context.Context, sql string) ([]map[string]json.RawMessage, error) {
	u := c.baseURL + sqlSearchPath + "?cmd=" + url.QueryEscape(sql) + "&format=json"
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var tables []wireTableResult
	if err := json.Unmarshal(body, &tables); err != nil {
		return nil, fmt.Errorf("decode sql response: %w", err)
	}
	// find Table1
	for _, t := range tables {
		if t.TableName == "Table1" {
			out := make([]map[string]json.RawMessage, 0, len(t.Rows))
			for _, raw := range t.Rows {
				var row map[string]json.RawMessage
				if err := json.Unmarshal(raw, &row); err != nil {
					return nil, fmt.Errorf("decode row: %w", err)
				}
				out = append(out, row)
			}
			return out, nil
		}
	}
	return nil, nil
}

// SearchPhotometry queries the PhotoObj table for sources with R-band magnitude
// between minMag and maxMag, returning at most limit records.
func (c *Client) SearchPhotometry(ctx context.Context, minMag, maxMag float64, limit int) ([]*PhotoObject, error) {
	sql := fmt.Sprintf(
		"SELECT TOP %d objid,ra,dec,type,r,g,u,i,z FROM PhotoObj WHERE r BETWEEN %g AND %g",
		limit, minMag, maxMag,
	)
	u := c.baseURL + sqlSearchPath + "?cmd=" + url.QueryEscape(sql) + "&format=json"
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var tables []wireTableResult
	if err := json.Unmarshal(body, &tables); err != nil {
		return nil, fmt.Errorf("decode photometry response: %w", err)
	}
	for _, t := range tables {
		if t.TableName != "Table1" {
			continue
		}
		out := make([]*PhotoObject, 0, len(t.Rows))
		for _, raw := range t.Rows {
			var w wirePhotoObj
			if err := json.Unmarshal(raw, &w); err != nil {
				return nil, fmt.Errorf("decode PhotoObj row: %w", err)
			}
			out = append(out, toPhotoObject(w))
		}
		return out, nil
	}
	return nil, nil
}

// SearchSpectra queries the SpecObj table for spectra of the given class
// (GALAXY, QSO, or STAR) with redshift between minZ and maxZ.
func (c *Client) SearchSpectra(ctx context.Context, class string, minZ, maxZ float64, limit int) ([]*Spectrum, error) {
	sql := fmt.Sprintf(
		"SELECT TOP %d specobjid,ra,dec,z,zErr,class,subClass FROM SpecObj WHERE class='%s' AND z BETWEEN %g AND %g",
		limit, class, minZ, maxZ,
	)
	u := c.baseURL + sqlSearchPath + "?cmd=" + url.QueryEscape(sql) + "&format=json"
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var tables []wireTableResult
	if err := json.Unmarshal(body, &tables); err != nil {
		return nil, fmt.Errorf("decode spectra response: %w", err)
	}
	for _, t := range tables {
		if t.TableName != "Table1" {
			continue
		}
		out := make([]*Spectrum, 0, len(t.Rows))
		for _, raw := range t.Rows {
			var w wireSpecObj
			if err := json.Unmarshal(raw, &w); err != nil {
				return nil, fmt.Errorf("decode SpecObj row: %w", err)
			}
			out = append(out, toSpectrum(w))
		}
		return out, nil
	}
	return nil, nil
}

// NearestObjects returns photometric objects within radius arcminutes of the
// given (ra, dec) sky coordinates using the SDSS cone-search table function.
func (c *Client) NearestObjects(ctx context.Context, ra, dec, radius float64, limit int) ([]*PhotoObject, error) {
	sql := fmt.Sprintf(
		"SELECT TOP %d p.objid,p.ra,p.dec,p.type,p.r,p.g,p.u,p.i,p.z FROM PhotoObj p JOIN dbo.fGetNearbyObjEq(%g,%g,%g) n ON p.objID=n.objID",
		limit, ra, dec, radius,
	)
	u := c.baseURL + sqlSearchPath + "?cmd=" + url.QueryEscape(sql) + "&format=json"
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var tables []wireTableResult
	if err := json.Unmarshal(body, &tables); err != nil {
		return nil, fmt.Errorf("decode nearby response: %w", err)
	}
	for _, t := range tables {
		if t.TableName != "Table1" {
			continue
		}
		out := make([]*PhotoObject, 0, len(t.Rows))
		for _, raw := range t.Rows {
			var w wirePhotoObj
			if err := json.Unmarshal(raw, &w); err != nil {
				return nil, fmt.Errorf("decode nearby row: %w", err)
			}
			out = append(out, toPhotoObject(w))
		}
		return out, nil
	}
	return nil, nil
}
