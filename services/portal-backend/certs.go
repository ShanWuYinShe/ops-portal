package main

import (
	"database/sql"
	"errors"
	"time"
)

// 证书库：portal 本地权威登记表（SQLite certs 表）。
// 发证流程：人工在 CF 面板/调 CF API 建证 → portal 登记（cf_id/serial/指纹/CN/有效期）。
// 生产门禁认"本地库 active + 边缘 verified"双条件；吊销 = 本地 revoke + 去 CF 吊销。

type certRecord struct {
	ID          string `json:"id"`          // 本地 id
	Label       string `json:"label"`       // 持有人-设备名，如 liyou-mbpro
	Kind        string `json:"kind"`        // human / agent / service / site
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

func scanCert(row *sql.Row) (*certRecord, error) {
	return scanCertRows(row)
}

func scanCertRows(row interface {
	Scan(dest ...any) error
}) (*certRecord, error) {
	c := &certRecord{}
	err := row.Scan(&c.ID, &c.Label, &c.Kind, &c.CFID, &c.Serial, &c.Fingerprint,
		&c.CommonName, &c.Issuer, &c.Status, &c.ExpiresAt, &c.CreatedAt, &c.RevokedAt, &c.Note)
	if err != nil {
		return nil, err
	}
	return c, nil
}

const certColumns = `id,label,kind,cf_id,serial,fingerprint,common_name,issuer,status,expires_at,created_at,revoked_at,note`

// findActiveCert 按 serial 或指纹找有效证书（门禁查表用）。
// hex 串大小写不敏感：CF 透传的 serial 是大写，建证 API 返回小写。
func findActiveCert(serial, fingerprint string) *certRecord {
	db, err := getDB()
	if err != nil {
		return nil
	}
	c, err := scanCert(db.QueryRow(`SELECT `+certColumns+` FROM certs WHERE status='active'
		AND ((? <> '' AND serial = ? COLLATE NOCASE) OR (? <> '' AND fingerprint = ? COLLATE NOCASE))
		LIMIT 1`, serial, serial, fingerprint, fingerprint))
	if err != nil {
		return nil
	}
	return c
}

func listCerts() []certRecord {
	db, err := getDB()
	if err != nil {
		return []certRecord{}
	}
	rows, err := db.Query(`SELECT ` + certColumns + ` FROM certs ORDER BY created_at DESC`)
	if err != nil {
		return []certRecord{}
	}
	defer rows.Close()
	out := []certRecord{}
	for rows.Next() {
		c, err := scanCertRows(rows)
		if err != nil {
			return []certRecord{}
		}
		out = append(out, *c)
	}
	return out
}

func addCert(c certRecord) (*certRecord, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if c.Status == "" {
		c.Status = "active"
	}
	_, err = db.Exec(`INSERT INTO certs (`+certColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Label, c.Kind, c.CFID, c.Serial, c.Fingerprint, c.CommonName,
		c.Issuer, c.Status, c.ExpiresAt, c.CreatedAt, c.RevokedAt, c.Note)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, errors.New("duplicate id")
		}
		return nil, err
	}
	return &c, nil
}

func revokeCert(id string) (*certRecord, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	res, err := db.Exec(`UPDATE certs SET status='revoked', revoked_at=? WHERE id=? AND status <> 'revoked'`,
		time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 已吊销则幂等返回，真正不存在才报错（与旧语义一致）
		c, err := scanCert(db.QueryRow(`SELECT `+certColumns+` FROM certs WHERE id=?`, id))
		if err != nil {
			return nil, errors.New("not found")
		}
		return c, nil
	}
	return scanCert(db.QueryRow(`SELECT `+certColumns+` FROM certs WHERE id=?`, id))
}

// certIDExists 证书 id 是否存在（任意状态，用户绑定时防 typo）。
func certIDExists(id string) bool {
	db, err := getDB()
	if err != nil {
		return false
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM certs WHERE id=?`, id).Scan(&n); err != nil {
		return false
	}
	return n > 0
}
