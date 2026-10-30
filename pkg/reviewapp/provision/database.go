package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ops/pkg/reviewapp"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/jackc/pgx/v5"
)

// DB is the part of a Postgres connection the controller uses.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) error
	QueryBool(ctx context.Context, sql string, args ...any) (bool, error)
	QueryString(ctx context.Context, sql string, args ...any) (string, error)
	Close(ctx context.Context) error
}

// Connector opens a connection to a database URL.
type Connector func(ctx context.Context, url string) (DB, error)

// PgxConnect is the Connector the controller uses in a task.
func PgxConnect(ctx context.Context, url string) (DB, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	return pgxDB{conn}, nil
}

type pgxDB struct{ conn *pgx.Conn }

func (d pgxDB) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.conn.Exec(ctx, sql, args...)
	return err
}

func (d pgxDB) QueryBool(ctx context.Context, sql string, args ...any) (bool, error) {
	var v bool
	err := d.conn.QueryRow(ctx, sql, args...).Scan(&v)
	return v, err
}

func (d pgxDB) QueryString(ctx context.Context, sql string, args ...any) (string, error) {
	var v string
	err := d.conn.QueryRow(ctx, sql, args...).Scan(&v)
	return v, err
}

func (d pgxDB) Close(ctx context.Context) error { return d.conn.Close(ctx) }

// dbRoles returns the shared roles and their passwords from the db secret:
// every <ROLE>_PASSWORD key is a role (D2).
func dbRoles(secret map[string]string) map[string]string {
	roles := map[string]string{}
	for k, v := range secret {
		if role, ok := strings.CutSuffix(k, "_PASSWORD"); ok && v != "" {
			roles[strings.ToLower(role)] = v
		}
	}
	return roles
}

func (c *Controller) secretJSON(ctx context.Context, name string) (map[string]string, error) {
	out, err := c.Secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(name)})
	if err != nil {
		return nil, fmt.Errorf("read secret %s: %w", name, err)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(aws.ToString(out.SecretString)), &v); err != nil {
		return nil, fmt.Errorf("parse secret %s: %w", name, err)
	}
	return v, nil
}

// admin connects as the RDS master user. Only the controller task role can
// read that secret.
func (c *Controller) admin(ctx context.Context) (DB, error) {
	secret, err := c.secretJSON(ctx, c.Infra.Secrets.RDSAdmin)
	if err != nil {
		return nil, err
	}
	url := secret["DATABASE_URL"]
	if url == "" {
		return nil, fmt.Errorf("secret %s has no DATABASE_URL", c.Infra.Secrets.RDSAdmin)
	}
	db, err := c.Connect(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to RDS as the admin user: %w", err)
	}
	return db, nil
}

// ensureDatabase makes sure the shared roles exist with the passwords in the
// db secret, then creates reviewapp_pr_<n> owned by the owner role and lets
// the other roles connect. Schema grants come from the migrations.
func (c *Controller) ensureDatabase(ctx context.Context, pr int) (string, error) {
	secret, err := c.secretJSON(ctx, c.Infra.Secrets.DB)
	if err != nil {
		return "", err
	}
	roles := dbRoles(secret)
	owner := c.Blueprint.DBOwnerRole
	if _, ok := roles[owner]; !ok {
		return "", fmt.Errorf("secret %s has no %s_PASSWORD for the owner role %s", c.Infra.Secrets.DB, strings.ToUpper(owner), owner)
	}

	db, err := c.admin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close(ctx) }()

	names := make([]string, 0, len(roles))
	for r := range roles {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, role := range names {
		exists, err := db.QueryBool(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role)
		if err != nil {
			return "", fmt.Errorf("look up role %s: %w", role, err)
		}
		verb := "ALTER"
		if !exists {
			verb = "CREATE"
		}
		// format() quotes the name and the password on the server.
		if err := execFormatted(ctx, db, `SELECT format('`+verb+` ROLE %I WITH LOGIN PASSWORD %L', $1::text, $2::text)`, role, roles[role]); err != nil {
			return "", fmt.Errorf("%s role %s: %w", strings.ToLower(verb), role, err)
		}
	}

	// On RDS the master user must be a member of a role to create a
	// database it owns.
	if err := execFormatted(ctx, db, `SELECT format('GRANT %I TO %I', $1::text, current_user)`, owner); err != nil {
		return "", fmt.Errorf("grant %s to the admin user: %w", owner, err)
	}

	name := reviewapp.Database(pr)
	exists, err := db.QueryBool(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name)
	if err != nil {
		return "", fmt.Errorf("look up database %s: %w", name, err)
	}
	if !exists {
		c.logf("Creating database %s", name)
		if err := execFormatted(ctx, db, `SELECT format('CREATE DATABASE %I OWNER %I', $1::text, $2::text)`, name, owner); err != nil {
			return "", fmt.Errorf("create database %s: %w", name, err)
		}
	}
	for _, role := range names {
		if role == owner {
			continue
		}
		if err := execFormatted(ctx, db, `SELECT format('GRANT CONNECT, TEMPORARY ON DATABASE %I TO %I', $1::text, $2::text)`, name, role); err != nil {
			return "", fmt.Errorf("grant %s on %s: %w", role, name, err)
		}
	}
	return name, nil
}

// dropDatabase drops reviewapp_pr_<n>, closing its connections. The shared
// roles stay.
func (c *Controller) dropDatabase(ctx context.Context, pr int) error {
	db, err := c.admin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(ctx) }()
	name := reviewapp.Database(pr)
	if err := execFormatted(ctx, db, `SELECT format('DROP DATABASE IF EXISTS %I WITH (FORCE)', $1::text)`, name); err != nil {
		return fmt.Errorf("drop database %s: %w", name, err)
	}
	return nil
}

// execFormatted renders a statement with format() on the server and runs it.
// DDL cannot take bind parameters, and format's %I and %L quote correctly.
func execFormatted(ctx context.Context, db DB, query string, args ...any) error {
	stmt, err := db.QueryString(ctx, query, args...)
	if err != nil {
		return err
	}
	return db.Exec(ctx, stmt)
}
