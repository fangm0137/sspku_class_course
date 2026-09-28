// Package seed implements explicit, serial development account initialization.
package seed

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"
)

var passwordKeys = [3]string{"SEED_TEACHER_A_PASSWORD", "SEED_STUDENT_A1_PASSWORD", "SEED_STUDENT_B1_PASSWORD"}

// Passwords has private fields so only validated input can reach Apply.
type Passwords struct{ values [3]string }

func LoadPasswords(getenv func(string) string) (Passwords, error) {
	var passwords Passwords
	for i, key := range passwordKeys {
		value := getenv(key)
		if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 128 {
			return Passwords{}, fmt.Errorf("invalid required configuration: %s", key)
		}
		passwords.values[i] = value
	}
	return passwords, nil
}

func passwordHash(password string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return "", errors.New("password hashing failed")
	}
	return fmt.Sprintf("$scrypt$32768$8$1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func Apply(ctx context.Context, pool *sql.DB, passwords Passwords) (bool, error) {
	// Reject the zero value as well, before any database access.
	for _, p := range passwords.values {
		if p == "" {
			return false, errors.New("seed passwords are required")
		}
	}
	failure := errors.New("account initialization failed")
	conflict := errors.New("existing account or class conflicts with development fixtures")
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return false, failure
	}
	defer tx.Rollback()
	changed := false
	classIDs := map[string]uint64{}
	for _, name := range []string{"A", "B"} {
		var count int
		var id sql.NullInt64
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),MIN(id) FROM classes WHERE name=?", name).Scan(&count, &id); err != nil {
			return false, failure
		}
		if count > 1 {
			return false, conflict
		}
		if count == 0 {
			result, e := tx.ExecContext(ctx, "INSERT INTO classes (name) VALUES (?)", name)
			if e != nil {
				return false, failure
			}
			value, e := result.LastInsertId()
			if e != nil {
				return false, failure
			}
			id.Int64 = value
			changed = true
		}
		classIDs[name] = uint64(id.Int64)
	}
	accounts := []struct{ username, role, class string }{{"teacher_a", "teacher", "A"}, {"student_a1", "student", "A"}, {"student_b1", "student", "B"}}
	for i, account := range accounts {
		var role string
		var classID uint64
		err = tx.QueryRowContext(ctx, "SELECT role,class_id FROM users WHERE username=?", account.username).Scan(&role, &classID)
		if err == nil {
			if role != account.role || classID != classIDs[account.class] {
				return false, conflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, failure
		}
		encoded, e := passwordHash(passwords.values[i])
		if e != nil {
			return false, e
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO users (username,password_hash,role,class_id) VALUES (?,?,?,?)", account.username, encoded, account.role, classIDs[account.class])
		if err != nil {
			return false, failure
		}
		changed = true
	}
	if err = tx.Commit(); err != nil {
		return false, failure
	}
	return changed, nil
}
