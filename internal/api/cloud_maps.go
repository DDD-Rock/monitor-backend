package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

const maxCloudMapUploadBytes = 16 << 20

type cloudMapUpload struct {
	FormatVersion int               `json:"formatVersion"`
	Maps          []json.RawMessage `json:"maps"`
}

type cloudMapName struct {
	MapName string `json:"mapName"`
}

func (s *Server) handleCloudMaps(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT m.id, m.map_name, OCTET_LENGTH(m.map_json),
		        CAST(UNIX_TIMESTAMP(m.updated_at) * 1000 AS SIGNED), u.username
		   FROM cloud_maps m
		   JOIN users u ON u.id = m.uploaded_by
		  ORDER BY m.map_name`)
	if err != nil {
		s.internalError(w, "list cloud maps failed", err)
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name, uploader string
		var size int
		var updatedAt int64
		if err := rows.Scan(&id, &name, &size, &updatedAt, &uploader); err != nil {
			s.internalError(w, "scan cloud map failed", err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "size": size, "updatedAt": updatedAt, "uploadedBy": uploader,
		})
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate cloud maps failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"maps": items})
}

func (s *Server) handleUploadCloudMaps(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCloudMapUploadBytes)
	decoder := json.NewDecoder(r.Body)
	var upload cloudMapUpload
	if err := decoder.Decode(&upload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_map_package", "地图文件格式无效或超过 16 MB")
		return
	}
	if upload.FormatVersion != 1 || len(upload.Maps) == 0 || len(upload.Maps) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_map_package", "地图文件版本无效或没有可上传的地图")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalError(w, "begin cloud map upload failed", err)
		return
	}
	defer tx.Rollback()

	for _, raw := range upload.Maps {
		if len(raw) == 0 || len(raw) > maxCloudMapUploadBytes {
			writeError(w, http.StatusBadRequest, "invalid_map", "地图数据为空或过大")
			return
		}
		var metadata cloudMapName
		if err := json.Unmarshal(raw, &metadata); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_map", "地图数据格式无效")
			return
		}
		metadata.MapName = strings.TrimSpace(metadata.MapName)
		if metadata.MapName == "" || len([]rune(metadata.MapName)) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_map_name", "地图名称不能为空且不能超过 128 个字符")
			return
		}
		_, err = tx.ExecContext(r.Context(),
			`INSERT INTO cloud_maps (map_name, map_json, uploaded_by)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE map_json = VALUES(map_json),
			                         uploaded_by = VALUES(uploaded_by),
			                         updated_at = CURRENT_TIMESTAMP(3)`,
			metadata.MapName, []byte(raw), mustUser(r.Context()).ID)
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1406 {
				writeError(w, http.StatusBadRequest, "map_too_large", "地图数据过大")
				return
			}
			s.internalError(w, "upsert cloud map failed", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.internalError(w, "commit cloud map upload failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploadedCount": len(upload.Maps)})
}

func (s *Server) handleDownloadCloudMap(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_map_id", "地图编号无效")
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT map_json FROM cloud_maps WHERE id = ?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "map_not_found", "云端地图不存在")
			return
		}
		s.internalError(w, "download cloud map failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="autobuff-map.json"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"formatVersion": 1,
		"maps":          []json.RawMessage{raw},
	})
}
