package compliance

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// SourceOFAC is the source tag written to sanctions_entities and
// sanctions_list_updates.
const SourceOFAC = "ofac_sdn"

// digitalCurrencyIDType is the prefix OFAC uses on <id> entries that carry a
// crypto address, e.g. "Digital Currency Address - XLM". Matching on the
// prefix rather than an exact string keeps new chains working without a code
// change.
const digitalCurrencyIDType = "digital currency address"

// SDNSource yields the raw SDN XML document. It is an interface so tests can
// serve a fixture from httptest without reaching the network, and so an
// operator can point at a mirror.
type SDNSource interface {
	Fetch(ctx context.Context) (io.ReadCloser, error)
}

// HTTPSDNSource downloads the SDN list over HTTP.
//
// The URL is operator-configured (OFAC_SDN_URL), never user-supplied, so it
// deliberately does not go through the SSRF guard that webhook destinations
// need — that guard is unexported inside internal/webhook and reusing it
// would mean extracting a shared package for no security gain here.
type HTTPSDNSource struct {
	url    string
	client *http.Client
}

func NewHTTPSDNSource(url string, client *http.Client) *HTTPSDNSource {
	if client == nil {
		// The real SDN file is tens of megabytes; the timeout covers the whole
		// download, not just the response headers.
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &HTTPSDNSource{url: url, client: client}
}

func (s *HTTPSDNSource) Fetch(ctx context.Context) (io.ReadCloser, error) {
	if s.url == "" {
		return nil, fmt.Errorf("OFAC_SDN_URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build sdn request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch sdn list: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch sdn list: unexpected status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// sdnEntry mirrors the subset of an <sdnEntry> element we care about.
type sdnEntry struct {
	UID       string   `xml:"uid"`
	FirstName string   `xml:"firstName"`
	LastName  string   `xml:"lastName"`
	SDNType   string   `xml:"sdnType"`
	Programs  []string `xml:"programList>program"`
	AKAList   []struct {
		FirstName string `xml:"firstName"`
		LastName  string `xml:"lastName"`
	} `xml:"akaList>aka"`
	IDList []struct {
		IDType   string `xml:"idType"`
		IDNumber string `xml:"idNumber"`
	} `xml:"idList>id"`
}

// ParseSDN streams the SDN XML and returns one entity per digital-currency
// address plus one per distinct name/alias.
//
// It walks tokens and decodes a single <sdnEntry> at a time rather than
// xml.Unmarshal-ing the whole document: the production file is ~40MB, and
// holding the entire parsed tree in memory alongside the rebuilt index would
// roughly double peak usage during a refresh.
func ParseSDN(r io.Reader) ([]*domain.SanctionsEntity, error) {
	decoder := xml.NewDecoder(r)
	refreshedAt := time.Now().UTC()

	var out []*domain.SanctionsEntity
	seen := make(map[string]struct{})

	add := func(uid, name, entityType, address, addressType string, programs []string) {
		name = strings.TrimSpace(name)
		if name == "" && address == "" {
			return
		}
		key := uid + "\x00" + normalizeName(name) + "\x00" + normalizeAddress(address)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, &domain.SanctionsEntity{
			UID:         uid,
			Name:        name,
			EntityType:  entityType,
			Address:     address,
			AddressType: addressType,
			Programs:    programs,
			Source:      SourceOFAC,
			RefreshedAt: refreshedAt,
		})
	}

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse sdn xml: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "sdnEntry" {
			continue
		}

		var e sdnEntry
		if err := decoder.DecodeElement(&e, &start); err != nil {
			return nil, fmt.Errorf("decode sdn entry: %w", err)
		}

		primary := joinName(e.FirstName, e.LastName)

		for _, id := range e.IDList {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(id.IDType)), digitalCurrencyIDType) {
				continue
			}
			if strings.TrimSpace(id.IDNumber) == "" {
				continue
			}
			add(e.UID, primary, e.SDNType, strings.TrimSpace(id.IDNumber), strings.TrimSpace(id.IDType), e.Programs)
		}

		add(e.UID, primary, e.SDNType, "", "", e.Programs)
		for _, aka := range e.AKAList {
			add(e.UID, joinName(aka.FirstName, aka.LastName), e.SDNType, "", "", e.Programs)
		}
	}

	return out, nil
}

func joinName(first, last string) string {
	first = strings.TrimSpace(first)
	last = strings.TrimSpace(last)
	switch {
	case first == "":
		return last
	case last == "":
		return first
	default:
		return first + " " + last
	}
}
