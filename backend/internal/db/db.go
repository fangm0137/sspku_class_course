package db

import (
	"database/sql"
	"errors"
	"net"
	"time"

	"campusclaw/backend/internal/config"
	"github.com/go-sql-driver/mysql"
)

// Open prepares a lazy pool; it deliberately does not ping MySQL.
// Process liveness must not depend on database availability.
func Open(cfg config.Database) (*sql.DB, error) {
	return open(cfg, false)
}

// OpenForMigration permits multiple statements only for trusted, embedded SQL.
func OpenForMigration(cfg config.Database) (*sql.DB, error) {
	return open(cfg, true)
}

func open(cfg config.Database, multiStatements bool) (*sql.DB, error) {
	driverConfig := mysql.NewConfig()
	driverConfig.User = cfg.User
	driverConfig.Passwd = cfg.Password
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(cfg.Host, cfg.Port)
	driverConfig.DBName = cfg.Name
	driverConfig.ParseTime = true
	driverConfig.MultiStatements = multiStatements
	driverConfig.Loc = time.UTC
	driverConfig.Params = map[string]string{"time_zone": "'+00:00'"}
	driverConfig.Timeout = 5 * time.Second
	driverConfig.ReadTimeout = 5 * time.Second
	driverConfig.WriteTimeout = 5 * time.Second
	connector, err := mysql.NewConnector(driverConfig)
	if err != nil {
		// Driver errors must not leak credentials or a connection string.
		return nil, errors.New("invalid database configuration")
	}
	return sql.OpenDB(connector), nil
}
