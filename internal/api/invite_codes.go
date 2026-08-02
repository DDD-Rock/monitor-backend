package api

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	defaultInviteDurationSeconds = int64(30 * 60)
	minimumInviteDurationSeconds = int64(60)
	maximumInviteDurationSeconds = int64(365 * 24 * 60 * 60)
)

type adminInviteCodeItem struct {
	ID                int64  `json:"id"`
	Code              string `json:"code"`
	CreatedAt         int64  `json:"createdAt"`
	ExpiresAt         int64  `json:"expiresAt"`
	UsedAt            *int64 `json:"usedAt"`
	CreatedByNickname string `json:"createdByNickname"`
	UsedByNickname    string `json:"usedByNickname,omitempty"`
}

func (s *Server) handleAdminInviteCodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(
		r.Context(),
		`SELECT ic.id, ic.code, ic.created_at, ic.expires_at, ic.used_at,
		        creator.nickname, COALESCE(consumer.nickname, '')
		 FROM invite_codes ic
		 JOIN users creator ON creator.id = ic.created_by
		 LEFT JOIN users consumer ON consumer.id = ic.used_by
		 ORDER BY ic.created_at DESC
		 LIMIT 100`,
	)
	if err != nil {
		s.internalError(w, "list invite codes failed", err)
		return
	}
	defer rows.Close()

	items := make([]adminInviteCodeItem, 0)
	for rows.Next() {
		item, err := scanAdminInviteCode(rows)
		if err != nil {
			s.internalError(w, "scan invite codes failed", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate invite codes failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inviteCodes": items})
}

func (s *Server) handleCreateAdminInviteCode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DurationSeconds *int64 `json:"durationSeconds"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	durationSeconds := defaultInviteDurationSeconds
	if request.DurationSeconds != nil {
		durationSeconds = *request.DurationSeconds
	}
	if durationSeconds < minimumInviteDurationSeconds || durationSeconds > maximumInviteDurationSeconds {
		writeError(w, http.StatusBadRequest, "invalid_invite_duration", "邀请码有效时长须在 1 分钟到 365 天之间")
		return
	}

	var item adminInviteCodeItem
	for attempt := 0; attempt < 5; attempt++ {
		code, err := newInviteCode()
		if err != nil {
			s.internalError(w, "generate invite code failed", err)
			return
		}
		result, err := s.db.ExecContext(
			r.Context(),
			`INSERT INTO invite_codes (code, created_by, expires_at)
			 VALUES (?, ?, DATE_ADD(NOW(3), INTERVAL ? SECOND))`,
			code,
			mustUser(r.Context()).ID,
			durationSeconds,
		)
		if err != nil {
			var mysqlError *mysql.MySQLError
			if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
				continue
			}
			s.internalError(w, "create invite code failed", err)
			return
		}
		id, _ := result.LastInsertId()
		row := s.db.QueryRowContext(
			r.Context(),
			`SELECT ic.id, ic.code, ic.created_at, ic.expires_at, ic.used_at,
			        creator.nickname, ''
			 FROM invite_codes ic
			 JOIN users creator ON creator.id = ic.created_by
			 WHERE ic.id = ?`,
			id,
		)
		item, err = scanAdminInviteCode(row)
		if err != nil {
			s.internalError(w, "load created invite code failed", err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
		return
	}
	s.internalError(w, "create unique invite code failed", fmt.Errorf("invite code collision limit reached"))
}

func (s *Server) handleDeleteAdminInviteCode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_invite_code_id", "邀请码编号无效")
		return
	}
	result, err := s.db.ExecContext(r.Context(), `DELETE FROM invite_codes WHERE id = ?`, id)
	if err != nil {
		s.internalError(w, "delete invite code failed", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "invite_code_not_found", "邀请码不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type inviteCodeScanner interface {
	Scan(dest ...any) error
}

func scanAdminInviteCode(scanner inviteCodeScanner) (adminInviteCodeItem, error) {
	var item adminInviteCodeItem
	var createdAt time.Time
	var expiresAt time.Time
	var usedAt sql.NullTime
	if err := scanner.Scan(
		&item.ID,
		&item.Code,
		&createdAt,
		&expiresAt,
		&usedAt,
		&item.CreatedByNickname,
		&item.UsedByNickname,
	); err != nil {
		return item, err
	}
	item.CreatedAt = createdAt.UnixMilli()
	item.ExpiresAt = expiresAt.UnixMilli()
	if usedAt.Valid {
		value := usedAt.Time.UnixMilli()
		item.UsedAt = &value
	}
	return item, nil
}

func newInviteCode() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 6)
	limit := big.NewInt(int64(len(alphabet)))
	for index := range result {
		value, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		result[index] = alphabet[value.Int64()]
	}
	return string(result), nil
}
