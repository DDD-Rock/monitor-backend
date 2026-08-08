package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		log.Fatal("DATABASE_DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	ensureColumn(db)
	ensureTable(db, "rope_teams", `CREATE TABLE rope_teams (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
        user_id BIGINT UNSIGNED NOT NULL,
        leader_session_id CHAR(36) NOT NULL,
        created_in_game TINYINT UNSIGNED NOT NULL DEFAULT 0,
        boss_role_name VARCHAR(24) NOT NULL DEFAULT '',
        boss_cycle_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
        boss_cycle_state VARCHAR(16) NOT NULL DEFAULT 'idle',
        created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
        updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
        PRIMARY KEY (id),
        UNIQUE KEY uk_rope_teams_user (user_id),
        CONSTRAINT fk_rope_teams_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
        CONSTRAINT fk_rope_teams_leader FOREIGN KEY (leader_session_id) REFERENCES monitor_sessions (id) ON DELETE CASCADE
    ) ENGINE=InnoDB`)
	ensureTable(db, "rope_team_members", `CREATE TABLE rope_team_members (
        team_id BIGINT UNSIGNED NOT NULL,
        session_id CHAR(36) NOT NULL,
        invited TINYINT UNSIGNED NOT NULL DEFAULT 0,
        boss_completed_cycle_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
        joined TINYINT UNSIGNED NOT NULL DEFAULT 0,
        joined_at DATETIME(3) NULL,
        PRIMARY KEY (team_id, session_id),
        CONSTRAINT fk_rope_team_members_team FOREIGN KEY (team_id) REFERENCES rope_teams (id) ON DELETE CASCADE,
        CONSTRAINT fk_rope_team_members_session FOREIGN KEY (session_id) REFERENCES monitor_sessions (id) ON DELETE CASCADE
    ) ENGINE=InnoDB`)
	ensureTable(db, "rope_join_receipts", `CREATE TABLE rope_join_receipts (
        receipt_id VARCHAR(64) NOT NULL,
        session_id CHAR(36) NOT NULL,
        created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
        PRIMARY KEY (receipt_id),
        KEY idx_rope_join_receipts_session (session_id),
        CONSTRAINT fk_rope_join_receipts_session FOREIGN KEY (session_id) REFERENCES monitor_sessions (id) ON DELETE CASCADE
    ) ENGINE=InnoDB`)
	ensureTableColumn(db, "rope_teams", "created_in_game", `ALTER TABLE rope_teams
        ADD COLUMN created_in_game TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER leader_session_id`)
	ensureTableColumn(db, "rope_team_members", "invited", `ALTER TABLE rope_team_members
        ADD COLUMN invited TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER session_id`)
	ensureTableColumn(db, "rope_teams", "boss_role_name", `ALTER TABLE rope_teams
        ADD COLUMN boss_role_name VARCHAR(24) NOT NULL DEFAULT '' AFTER created_in_game`)
	ensureTableColumn(db, "rope_teams", "boss_cycle_id", `ALTER TABLE rope_teams
        ADD COLUMN boss_cycle_id BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER boss_role_name`)
	ensureTableColumn(db, "rope_teams", "boss_cycle_state", `ALTER TABLE rope_teams
        ADD COLUMN boss_cycle_state VARCHAR(16) NOT NULL DEFAULT 'idle' AFTER boss_cycle_id`)
	ensureTableColumn(db, "rope_team_members", "boss_completed_cycle_id", `ALTER TABLE rope_team_members
        ADD COLUMN boss_completed_cycle_id BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER invited`)
	fmt.Println("rope party migration complete")
}

func ensureTableColumn(db *sql.DB, table, column, statement string) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`, table, column).Scan(&count)
	if err != nil {
		log.Fatal(err)
	}
	if count > 0 {
		fmt.Printf("%s.%s already exists\n", table, column)
		return
	}
	if _, err := db.Exec(statement); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created %s.%s\n", table, column)
}

func ensureColumn(db *sql.DB) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'monitor_sessions' AND COLUMN_NAME = 'role_name'`).Scan(&count)
	if err != nil {
		log.Fatal(err)
	}
	if count > 0 {
		fmt.Println("monitor_sessions.role_name already exists")
		return
	}
	if _, err := db.Exec(`ALTER TABLE monitor_sessions
        ADD COLUMN role_name VARCHAR(24) NOT NULL DEFAULT '' COMMENT '游戏角色名称' AFTER name`); err != nil {
		log.Fatal(err)
	}
	fmt.Println("created monitor_sessions.role_name")
}

func ensureTable(db *sql.DB, name, statement string) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, name).Scan(&count)
	if err != nil {
		log.Fatal(err)
	}
	if count > 0 {
		fmt.Printf("%s already exists\n", name)
		return
	}
	if _, err := db.Exec(statement); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created %s\n", name)
}
