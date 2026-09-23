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

// findActiveCert 按 serial 或指纹找有效证书（门禁查表用，每请求必过）。
// hex 串大小写不敏感：CF 透传的 serial 是大写，建证 API 返回小写。
//
// 性能：serial/指纹分别查再合并，刻意不用 `A OR B` 单查询——
// SQLite 对含 OR 的复合条件不做索引下推（实测 SCAN，5000 行 633µs），
// 拆成 UNION ALL 后两个分支各自命中 NOCASE 索引（13µs，约 48 倍）。
// 空串条件保留原语义（空串不参与匹配，防误命中）。
func findActiveCert(serial, fingerprint string) *certRecord {
	db, err := getDB()
	if err != nil {
		return nil
	}
	const q = `SELECT ` + certColumns + ` FROM certs
		WHERE status='active' AND serial <> '' AND serial = ? COLLATE NOCASE
		UNION ALL
		SELECT ` + certColumns + ` FROM certs
		WHERE status='active' AND fingerprint <> '' AND fingerprint = ? COLLATE NOCASE
		LIMIT 1`
	c, err := scanCert(db.QueryRow(q, serial, fingerprint))
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

// validCertKinds 证书 kind 枚举：登记面（certs/register）只收这四种。
// 空串在 API 层按 human 处理（schema 默认值的历史语义）。
var validCertKinds = map[string]bool{"human": true, "portal-agent": true, "service": true, "site": true}

// isHexStr 空 = 合法（fingerprint 允许缺省），非空必须是 hex（serial/指纹
// 都是 CF 返回的 hex 串；大小写皆可，门禁查表走 NOCASE）。
func isHexStr(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// activeCertDupe active 证书是否已有相同 serial 或指纹（NOCASE 对齐
// findActiveCert 的大小写不敏感命中语义）。重复登记会让门禁命中歧义。
func activeCertDupe(serial, fingerprint string) (bool, error) {
	db, err := getDB()
	if err != nil {
		return false, err
	}
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM certs WHERE status='active' AND (
		(serial <> '' AND serial = ? COLLATE NOCASE) OR
		(fingerprint <> '' AND fingerprint = ? COLLATE NOCASE))`,
		serial, fingerprint).Scan(&n)
	return n > 0, err
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

// getCertByID 按 id 查证书（任意状态）。用户绑定时校验 kind 用。
func getCertByID(id string) (*certRecord, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	return scanCert(db.QueryRow(`SELECT `+certColumns+` FROM certs WHERE id=?`, id))
}

// renameCert 改显示名（label）。认证按 serial/指纹查表不受影响。
// portal-agent 证书在数据层直接拒绝：label 即 portal_agent_id，是机器
// 双因子「证书在表且 label==上报身份」的绑定键，改名会断开身份校验。
func renameCert(id, label string) (*certRecord, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	old, err := scanCert(db.QueryRow(`SELECT `+certColumns+` FROM certs WHERE id=?`, id))
	if err != nil {
		return nil, errors.New("not found")
	}
	if old.Kind == "portal-agent" {
		return nil, errors.New("portal-agent 证书的 label 即 portal_agent_id（机器身份绑定键），不可改名")
	}
	if _, err := db.Exec(`UPDATE certs SET label=? WHERE id=?`, label, id); err != nil {
		return nil, err
	}
	return scanCert(db.QueryRow(`SELECT `+certColumns+` FROM certs WHERE id=?`, id))
}
