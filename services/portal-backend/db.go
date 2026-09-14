package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// 数据层：单文件 SQLite（dataDir()/portal.db），WAL 模式，单写连接。
// 旧版 JSON 文件首次启动时一次性导入（幂等：目标表非空则跳过），
// 成功后原文件改名 .migrated 备查。

const schema = `
CREATE TABLE IF NOT EXISTS certs (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'human',
  cf_id TEXT NOT NULL DEFAULT '',
  serial TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT '',
  common_name TEXT NOT NULL DEFAULT '',
  issuer TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  expires_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  revoked_at TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT ''
);
-- 证书查表必须走索引：serial/指纹是 hex 串，CF 透传大写、建证返回小写，
-- 故索引与查询都用 COLLATE NOCASE。BINARY 索引无法服务 NOCASE 比较
-- （实测退化为全表扫描：5000 行下单次查表 633µs），显式声明 collation 后 13µs。
CREATE INDEX IF NOT EXISTS idx_certs_serial_nocase ON certs(serial COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_certs_fingerprint_nocase ON certs(fingerprint COLLATE NOCASE);
-- 清理旧版的 BINARY 索引（服务不了 NOCASE 比较，留着只增写入成本）
DROP INDEX IF EXISTS idx_certs_serial;
DROP INDEX IF EXISTS idx_certs_fingerprint;
CREATE TABLE IF NOT EXISTS agent_tokens (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL DEFAULT '',
  hash TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  revoked INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL DEFAULT 'agent',
  expires_at TEXT NOT NULL DEFAULT '',
  used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tokens_hash ON agent_tokens(hash);
CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  role TEXT NOT NULL DEFAULT 'ops.viewer',
  cert_id TEXT NOT NULL DEFAULT ''
);
-- 每次请求都要按证书反查用户（currentUser），必须走索引
CREATE INDEX IF NOT EXISTS idx_users_cert_id ON users(cert_id);
CREATE TABLE IF NOT EXISTS sites (
  name TEXT PRIMARY KEY,
  ops_url TEXT NOT NULL DEFAULT '',
  cert_serial TEXT NOT NULL DEFAULT '',
  registered_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS commands (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  service TEXT NOT NULL DEFAULT '',
  project TEXT NOT NULL DEFAULT '',
  targets TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'queued',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_commands_agent ON commands(agent_id, status);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL DEFAULT '',
  actor TEXT NOT NULL DEFAULT '',
  cert TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  object TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  remote_ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_id ON audit(id);
`

func dbPath() string { return dataDir() + "/portal.db" }

var dbState = struct {
	sync.Mutex
	db  *sql.DB
	dir string
}{}

// getDB 按当前 dataDir() 打开（并缓存）数据库句柄。
// 目录变化时（单测）关闭旧句柄重开；生产常驻单一句柄。
func getDB() (*sql.DB, error) {
	dir := dataDir()
	dbState.Lock()
	defer dbState.Unlock()
	if dbState.db != nil && dbState.dir == dir {
		return dbState.db, nil
	}
	if dbState.db != nil {
		_ = dbState.db.Close()
		dbState.db = nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	migrateLegacyJSON(db, dir)
	dbState.db, dbState.dir = db, dir
	return db, nil
}

func tableEmpty(db *sql.DB, table string) bool {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		return false
	}
	return n == 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// 以下为旧版 JSON 文件结构，仅迁移时解析用（db.go migrateLegacyJSON）。
type certStore struct {
	Certs []certRecord `json:"certs"`
}

type tokenStore struct {
	Tokens []agentToken `json:"tokens"`
}

type userStore struct {
	Users []portalUser `json:"users"`
}

type commandStore struct {
	Commands []portalCommand `json:"commands"`
}

// isUniqueViolation 主键/唯一冲突（addCert 的 duplicate id 等）。
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// migrateLegacyJSON 旧版 JSON 文件一次性导入：表空且文件有数据才导，
// 全部成功后原文件改名 .migrated。失败只记日志，不挡启动。
func migrateLegacyJSON(db *sql.DB, dir string) {
	done := map[string]string{} // table -> legacy path
	defer func() {
		for table, path := range done {
			if err := os.Rename(path, path+".migrated"); err != nil {
				log.Printf("db: rename %s failed: %v", path, err)
			} else {
				log.Printf("db: migrated %s -> %s table", path, table)
			}
		}
	}()
	migrate := func(table, file string, fn func() (int, error)) {
		path := dir + "/" + file
		if _, err := os.Stat(path); err != nil {
			return
		}
		if !tableEmpty(db, table) {
			return
		}
		n, err := fn()
		if err != nil {
			log.Printf("db: migrate %s failed: %v", path, err)
			return
		}
		log.Printf("db: migrated %d rows %s -> %s", n, path, table)
		done[table] = path
	}

	migrate("certs", "certs.json", func() (int, error) {
		s := &certStore{}
		if err := readJSON(dir+"/certs.json", s); err != nil {
			return 0, err
		}
		tx, err := db.Begin()
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		for _, c := range s.Certs {
			if c.Status == "" {
				c.Status = "active"
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO certs
				(id,label,kind,cf_id,serial,fingerprint,common_name,issuer,status,expires_at,created_at,revoked_at,note)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				c.ID, c.Label, c.Kind, c.CFID, c.Serial, c.Fingerprint, c.CommonName,
				c.Issuer, c.Status, c.ExpiresAt, c.CreatedAt, c.RevokedAt, c.Note); err != nil {
				return 0, err
			}
		}
		return len(s.Certs), tx.Commit()
	})

	migrate("agent_tokens", "agent_tokens.json", func() (int, error) {
		s := &tokenStore{}
		if err := readJSON(dir+"/agent_tokens.json", s); err != nil {
			return 0, err
		}
		tx, err := db.Begin()
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		for _, t := range s.Tokens {
			if t.Kind == "" {
				t.Kind = "agent" // 旧库存量正式 token 无 kind 字段
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO agent_tokens
				(id,agent_id,hash,created_by,created_at,revoked,kind,expires_at,used)
				VALUES (?,?,?,?,?,?,?,?,?)`,
				t.ID, t.AgentID, t.Hash, t.CreatedBy, t.CreatedAt,
				boolInt(t.Revoked), t.Kind, t.ExpiresAt, boolInt(t.Used)); err != nil {
				return 0, err
			}
		}
		return len(s.Tokens), tx.Commit()
	})

	migrate("users", "users.json", func() (int, error) {
		s := &userStore{}
		if err := readJSON(dir+"/users.json", s); err != nil {
			return 0, err
		}
		tx, err := db.Begin()
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		for _, u := range s.Users {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO users (username,role,cert_id) VALUES (?,?,?)`,
				u.Username, u.Role, u.CertID); err != nil {
				return 0, err
			}
		}
		return len(s.Users), tx.Commit()
	})

	migrate("sites", "sites.json", func() (int, error) {
		s := &siteRegistry{}
		if err := readJSON(dir+"/sites.json", s); err != nil {
			return 0, err
		}
		tx, err := db.Begin()
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		for _, e := range s.Sites {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO sites (name,ops_url,cert_serial,registered_at) VALUES (?,?,?,?)`,
				e.Name, e.OpsURL, e.CertSerial, e.RegisteredAt); err != nil {
				return 0, err
			}
		}
		return len(s.Sites), tx.Commit()
	})

	migrate("commands", "commands.json", func() (int, error) {
		s := &commandStore{}
		if err := readJSON(dir+"/commands.json", s); err != nil {
			return 0, err
		}
		tx, err := db.Begin()
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		for _, c := range s.Commands {
			tg, _ := marshalTargets(c.Targets)
			if _, err := tx.Exec(`INSERT OR IGNORE INTO commands
				(id,agent_id,action,service,project,targets,status,created_by,created_at,result)
				VALUES (?,?,?,?,?,?,?,?,?,?)`,
				c.ID, c.AgentID, c.Action, c.Service, c.Project, tg,
				c.Status, c.CreatedBy, c.CreatedAt, c.Result); err != nil {
				return 0, err
			}
		}
		return len(s.Commands), tx.Commit()
	})

	migrate("audit", "audit.jsonl", func() (int, error) {
		f, err := os.Open(dir + "/audit.jsonl")
		if err != nil {
			return 0, err
		}
		defer f.Close()
		tx, err := db.Begin()
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		n := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var e auditEntry
			if json.Unmarshal(append([]byte{}, sc.Bytes()...), &e) != nil {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO audit
				(at,actor,cert,action,object,result,note,remote_ip)
				VALUES (?,?,?,?,?,?,?,?)`,
				e.At, e.Actor, e.CertDesc, e.Action, e.Object, e.Result, e.Note, e.IP); err != nil {
				return 0, err
			}
			n++
		}
		if err := sc.Err(); err != nil {
			return 0, err
		}
		return n, tx.Commit()
	})
}

// auditRetentionDays 审计保留天数（默认 180；非法值回退默认）。
func auditRetentionDays() int {
	n, err := strconv.Atoi(getEnv("AUDIT_RETENTION_DAYS", "180"))
	if err != nil || n <= 0 {
		return 180
	}
	return n
}

// purgeOldAudit 按保留期清理审计（启动 + 备份前调用）。
func purgeOldAudit() {
	db, err := getDB()
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -auditRetentionDays()).UTC().Format(time.RFC3339)
	res, err := db.Exec(`DELETE FROM audit WHERE at < ?`, cutoff)
	if err != nil {
		log.Printf("db: purge audit failed: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("db: purged %d audit rows older than %d days", n, auditRetentionDays())
	}
}

// backupDB 在线备份：VACUUM INTO 快照 + 只留最新 14 份。返回备份文件名。
func backupDB() (string, error) {
	db, err := getDB()
	if err != nil {
		return "", err
	}
	purgeOldAudit()
	dir := dataDir() + "/backups"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := "portal-" + time.Now().UTC().Format("20060102-150405") + ".db"
	// VACUUM INTO 不接受占位符，路径为服务端内部拼接（时间戳），无注入面。
	if _, err := db.Exec(`VACUUM INTO '` + dir + `/` + name + `'`); err != nil {
		return "", fmt.Errorf("vacuum into: %w", err)
	}
	_ = os.Chmod(dir+"/"+name, 0600)
	entries, _ := filepath.Glob(dir + "/portal-*.db")
	for i := 0; i+14 < len(entries); i++ {
		_ = os.Remove(entries[i]) // Glob 按名字升序 = 按时间升序，删最老的
	}
	return name, nil
}
