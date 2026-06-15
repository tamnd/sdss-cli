package sdss

import (
	"context"
	"fmt"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes the SDSS data as a kit Domain: a driver that a multi-domain
// host enables with a single blank import,
//
//	import _ "github.com/tamnd/sdss-cli/sdss"
//
// The init below registers it; the same Domain also builds the standalone sdss
// binary, so the binary and any host share one source of truth.
func init() { kit.Register(Domain{}) }

// Domain is the SDSS driver. It carries no state.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against,
// and the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "sdss",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "sdss",
			Short:  "A command line for the Sloan Digital Sky Survey.",
			Long: `A command line for the Sloan Digital Sky Survey (SDSS DR18).

sdss reads public SDSS data over plain HTTPS, shapes it into clean records,
and prints output that pipes into the rest of your tools. No API key required.
1.2 billion photometric objects and 5.1 million spectra.`,
			Site: Host,
			Repo: "https://github.com/tamnd/sdss-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	// photometry: search objects by R-band magnitude range.
	kit.Handle(app, kit.OpMeta{Name: "photometry", Group: "read", List: true,
		Summary: "Search photometric objects by R-band magnitude range (--min-mag, --max-mag, --limit)"},
		listPhotometry)

	// spectra: search spectra by class and redshift range.
	kit.Handle(app, kit.OpMeta{Name: "spectra", Group: "read", List: true,
		Summary: "Search spectra by class and redshift range (--class, --min-z, --max-z, --limit)"},
		listSpectra)

	// nearby: objects near sky coordinates.
	kit.Handle(app, kit.OpMeta{Name: "nearby", Group: "read", List: true,
		Summary: "Find photometric objects near sky coordinates (--radius, --limit)",
		Args: []kit.Arg{
			{Name: "ra", Help: "right ascension in degrees"},
			{Name: "dec", Help: "declination in degrees"},
		}},
		listNearby)

	// sql: raw SQL query against SDSS.
	kit.Handle(app, kit.OpMeta{Name: "sql", Group: "read", List: true,
		Summary: "Run a raw SQL query against the SDSS SkyServer",
		Args:    []kit.Arg{{Name: "query", Help: "SQL query string"}}},
		runSQL)
}

// newClient builds the SDSS client from the host-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// --- inputs ---

type photometryInput struct {
	MinMag float64 `kit:"flag" help:"minimum R-band magnitude"`
	MaxMag float64 `kit:"flag" help:"maximum R-band magnitude"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type spectraInput struct {
	Class  string  `kit:"flag" help:"spectral class (GALAXY, QSO, STAR)"`
	MinZ   float64 `kit:"flag" help:"minimum redshift"`
	MaxZ   float64 `kit:"flag" help:"maximum redshift"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type nearbyInput struct {
	RA     float64 `kit:"arg"  help:"right ascension in degrees"`
	Dec    float64 `kit:"arg"  help:"declination in degrees"`
	Radius float64 `kit:"flag" help:"search radius in arcminutes"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type sqlInput struct {
	Query  string  `kit:"arg"          help:"SQL query string"`
	Client *Client `kit:"inject"`
}

// sqlRow is a generic map row emitted by the sql command.
type sqlRow struct {
	ID   string            `json:"id"   kit:"id"`
	Data map[string]string `json:"data"`
}

// --- handlers ---

func listPhotometry(ctx context.Context, in photometryInput, emit func(*PhotoObject) error) error {
	minMag := in.MinMag
	if minMag == 0 {
		minMag = 14
	}
	maxMag := in.MaxMag
	if maxMag == 0 {
		maxMag = 16
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	objs, err := in.Client.SearchPhotometry(ctx, minMag, maxMag, limit)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := emit(o); err != nil {
			return err
		}
	}
	return nil
}

func listSpectra(ctx context.Context, in spectraInput, emit func(*Spectrum) error) error {
	class := in.Class
	if class == "" {
		class = "GALAXY"
	}
	maxZ := in.MaxZ
	if maxZ == 0 {
		maxZ = 1
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	specs, err := in.Client.SearchSpectra(ctx, class, in.MinZ, maxZ, limit)
	if err != nil {
		return err
	}
	for _, s := range specs {
		if err := emit(s); err != nil {
			return err
		}
	}
	return nil
}

func listNearby(ctx context.Context, in nearbyInput, emit func(*PhotoObject) error) error {
	radius := in.Radius
	if radius == 0 {
		radius = 1
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	objs, err := in.Client.NearestObjects(ctx, in.RA, in.Dec, radius, limit)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := emit(o); err != nil {
			return err
		}
	}
	return nil
}

func runSQL(ctx context.Context, in sqlInput, emit func(*sqlRow) error) error {
	rows, err := in.Client.QuerySQL(ctx, in.Query)
	if err != nil {
		return err
	}
	for i, row := range rows {
		r := &sqlRow{
			ID:   fmt.Sprintf("%d", i),
			Data: make(map[string]string, len(row)),
		}
		for k, v := range row {
			r.Data[k] = string(v)
		}
		if err := emit(r); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: pure, network-free string functions ---

// Classify turns any non-empty input into ("object", input), since SDSS
// does not have a canonical URI scheme beyond object IDs.
func (Domain) Classify(input string) (uriType, id string, err error) {
	if input == "" {
		return "", "", errs.Usage("empty SDSS reference")
	}
	return "object", input, nil
}

// Locate returns the SDSS SkyServer detail URL for a given object ID.
func (Domain) Locate(uriType, id string) (string, error) {
	if uriType != "object" {
		return "", errs.Usage("sdss has no resource type %q", uriType)
	}
	return "https://" + Host + "/dr18/SkyServerWS/SearchTools/GetObjDetailStr?objId=" + id, nil
}
