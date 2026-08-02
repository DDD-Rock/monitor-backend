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
        joined TINYINT UNSIGNED NOT NULL DEFAULT 0,
        joined_at DATETIME(3) NULL,
        PRIMARY KEY (team_id, session_id),
        CONSTRAINT fk_rope_team_members_team FOREIGN KEY (team_id) REFERENCES rope_teams (id) ON DELETE CASCADE,
        CONSTRAINT fk_rope_team_members_session FOREIGN KEY (session_id) REFERENCES monitor_sessions (id) ON DELETE CASCADE
    ) ENGINE=InnoDB`)
	fmt.Println("rope party migration complete")
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
