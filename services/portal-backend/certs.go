package main

import (
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// 证书库：portal 本地权威登记表。
// 发证流程：人工在 CF 面板/调 CF API 建证 → portal 登记（cf_id/serial/指纹/CN/有效期）。
// 生产门禁认"本地库 active + 边缘 verified"双条件；吊销 = 本地 revoke + 去 CF 吊销。

type certRecord struct {
	ID          string `json:"id"`          // 本地 id
	Label       string `json:"label"`       // 持有人-设备名，如 liyou-mbpro
	Kind        string `json:"kind"`        // human / agent
	CFID        string `json:"cf_id"`       // Cloudflare 证书 id
	Serial      string `json:"serial"`      // CF 返回的 serial_number
	Fingerprint string `json:"fingerprint"` // fingerprint_sha256
	CommonName  string `json:"common_name"`
	Issuer      string `json:"issuer"` // 预留：托管 CA 或未来 BYOCA
	Status      string `json:"status"` // active / revoked
	ExpiresAt   string `json:"expires_at"`
	CreatedAt   string `json:"created_at"`
	RevokedAt   string `json:"revoked_at"`
	Note        string `json:"note"`
}

type certStore struct {
	Certs []certRecord `json:"certs"`
}

var certsMu sync.Mutex

func certsPath() string {
	return dataDir() + "/certs.json"
}

func loadCerts() (*certStore, error) {
	s := &certStore{}
	if err := readJSON(certsPath(), s); err != nil {
		if os.IsNotExist(err) {
			return &certStore{}, nil
		}
		return nil, err
	}
	if s.Certs == nil {
		s.Certs = []certRecord{}
	}
	return s, nil
}

func saveCerts(s *certStore) error {
	return atomicWriteJSON(certsPath(), s, 0600)
}

// findActiveCert 按 serial 或指纹找有效证书（门禁查表用）。
// hex 串比较用 EqualFold：CF 透传的 serial 是大写，建证 API 返回小写。
func findActiveCert(serial, fingerprint string) *certRecord {
	certsMu.Lock()
	defer certsMu.Unlock()
	s, err := loadCerts()
	if err != nil {
		return nil
	}
	for i := range s.Certs {
		c := &s.Certs[i]
		if c.Status != "active" {
			continue
		}
		if serial != "" && strings.EqualFold(c.Serial, serial) {
			return c
		}
		if fingerprint != "" && strings.EqualFold(c.Fingerprint, fingerprint) {
			return c
		}
	}
	return nil
}

func listCerts() []certRecord {
	certsMu.Lock()
	defer certsMu.Unlock()
	s, err := loadCerts()
	if err != nil {
		return []certRecord{}
	}
	out := append([]certRecord{}, s.Certs...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func addCert(c certRecord) (*certRecord, error) {
	certsMu.Lock()
	defer certsMu.Unlock()
	s, err := loadCerts()
	if err != nil {
		return nil, err
	}
	for _, e := range s.Certs {
		if e.ID == c.ID {
			return nil, errors.New("duplicate id")
		}
	}
	c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if c.Status == "" {
		c.Status = "active"
	}
	s.Certs = append(s.Certs, c)
	if err := saveCerts(s); err != nil {
		return nil, err
	}
	return &c, nil
}

func revokeCert(id string) (*certRecord, error) {
	certsMu.Lock()
	defer certsMu.Unlock()
	s, err := loadCerts()
	if err != nil {
		return nil, err
	}
	for i := range s.Certs {
		if s.Certs[i].ID == id {
			if s.Certs[i].Status == "revoked" {
				return &s.Certs[i], nil
			}
			s.Certs[i].Status = "revoked"
			s.Certs[i].RevokedAt = time.Now().UTC().Format(time.RFC3339)
			if err := saveCerts(s); err != nil {
				return nil, err
			}
			return &s.Certs[i], nil
		}
	}
	return nil, errors.New("not found")
}
