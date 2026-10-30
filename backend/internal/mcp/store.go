package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

// 服务器状态。connected 只代表"最近一次发现成功"。
const (
	StatusDisconnected = "disconnected"
	StatusConnected    = "connected"
	StatusError        = "error"
)

// 长度上限与数据库 CHECK 保持一致。
const (
	MaxNameRunes  = 64
	MaxURLRunes   = 255
	MaxErrorRunes = 255
)

var (
	// ErrNotFound 覆盖"不存在"与"不属于本班"，调用方统一返回 404。
	ErrNotFound = errors.New("mcp server not found")
	// ErrNameTaken 表示班内服务器名称重复。
	ErrNameTaken = errors.New("mcp server name already used in this class")
	// ErrInvalid 表示字段不满足约束，文案可直接给教师看。
	ErrInvalid = errors.New("invalid mcp server field")
	// ErrSecret 表示令牌无法解密（通常因为 TOKEN_SECRET 被换过）。
	ErrSecret = errors.New("mcp token cannot be decrypted")
)

// Server 是一台登记过的 MCP 服务器。令牌只以 HasToken 的形式对外。
type Server struct {
	ID          uint64
	ClassID     uint64
	Name        string
	BaseURL     string
	HasToken    bool
	Status      string
	LastError   string
	ConnectedAt *time.Time
	ToolCount   int
}

// Declaration 是一条可发给模型的工具声明。四项全部来自发现结果，不手写、不另存。
type Declaration struct {
	Ref         string
	ServerID    uint64
	ServerName  string
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Store 是 MCP 服务器与工具缓存的存储层。
type Store struct {
	db      *sql.DB
	box     *cipherBox
	timeout time.Duration
}

// NewStore 构造存储层。密钥长度不对时返回错误，而不是让"存令牌"在运行期随机失败。
func NewStore(db *sql.DB, settings Settings) (*Store, error) {
	box, err := newCipherBox(settings.TokenKey)
	if err != nil {
		return nil, err
	}
	timeout := settings.CallTimeout
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	return &Store{db: db, box: box, timeout: timeout}, nil
}

// List 返回本班服务器，含工具条数。
func (s *Store) List(ctx context.Context, classID uint64) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id,s.class_id,s.name,s.base_url,s.auth_token_cipher IS NOT NULL,s.status,
		        IFNULL(s.last_error,''),s.connected_at,
		        (SELECT COUNT(*) FROM mcp_tools t WHERE t.server_id=s.id)
		 FROM mcp_servers s WHERE s.class_id=? ORDER BY s.id`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Server, 0, 4)
	for rows.Next() {
		var item Server
		var connectedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.ClassID, &item.Name, &item.BaseURL, &item.HasToken,
			&item.Status, &item.LastError, &connectedAt, &item.ToolCount); err != nil {
			return nil, err
		}
		if connectedAt.Valid {
			value := connectedAt.Time
			item.ConnectedAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Get 读取本班单台服务器；跨班与不存在都返回 ErrNotFound。
func (s *Store) Get(ctx context.Context, classID, id uint64) (Server, error) {
	var item Server
	var connectedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT s.id,s.class_id,s.name,s.base_url,s.auth_token_cipher IS NOT NULL,s.status,
		        IFNULL(s.last_error,''),s.connected_at,
		        (SELECT COUNT(*) FROM mcp_tools t WHERE t.server_id=s.id)
		 FROM mcp_servers s WHERE s.id=? AND s.class_id=?`, id, classID).
		Scan(&item.ID, &item.ClassID, &item.Name, &item.BaseURL, &item.HasToken,
			&item.Status, &item.LastError, &connectedAt, &item.ToolCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, err
	}
	if connectedAt.Valid {
		value := connectedAt.Time
		item.ConnectedAt = &value
	}
	return item, nil
}

// Create 登记一台服务器。令牌必填并立即加密，明文不落库。
func (s *Store) Create(ctx context.Context, classID uint64, name, baseURL, token string) (uint64, error) {
	name, baseURL = strings.TrimSpace(name), strings.TrimSpace(baseURL)
	if err := validateServer(name, baseURL); err != nil {
		return 0, err
	}
	if strings.TrimSpace(token) == "" {
		return 0, fmt.Errorf("%w: 登记时必须提供令牌", ErrInvalid)
	}
	if _, err := NewClient(baseURL, token, s.timeout); err != nil {
		return 0, fmt.Errorf("%w: Base URL 必须是 http 或 https 地址，且不能内嵌账号密码", ErrInvalid)
	}
	cipherText, err := s.box.seal(token)
	if err != nil {
		return 0, ErrSecret
	}
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO mcp_servers (class_id,name,base_url,auth_token_cipher) VALUES (?,?,?,?)",
		classID, name, baseURL, cipherText)
	if err != nil {
		if isDuplicate(err) {
			return 0, ErrNameTaken
		}
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint64(id), nil
}

// Update 改写名称与地址；token 为空表示沿用原令牌。
// 地址或令牌变化会清空工具缓存并回到未连接：旧清单对应的可能是另一台服务。
func (s *Store) Update(ctx context.Context, classID, id uint64, name, baseURL, token string) error {
	name, baseURL = strings.TrimSpace(name), strings.TrimSpace(baseURL)
	if err := validateServer(name, baseURL); err != nil {
		return err
	}
	if _, err := NewClient(baseURL, token, s.timeout); err != nil {
		return fmt.Errorf("%w: Base URL 必须是 http 或 https 地址，且不能内嵌账号密码", ErrInvalid)
	}
	current, err := s.Get(ctx, classID, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 地址或令牌变了，旧清单就不再可信：清空缓存并回到未连接，等教师重新连接。
	changed := current.BaseURL != baseURL || strings.TrimSpace(token) != ""
	if strings.TrimSpace(token) != "" {
		cipherText, err := s.box.seal(token)
		if err != nil {
			return ErrSecret
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE mcp_servers SET name=?,base_url=?,auth_token_cipher=? WHERE id=? AND class_id=?",
			name, baseURL, cipherText, id, classID); err != nil {
			if isDuplicate(err) {
				return ErrNameTaken
			}
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			"UPDATE mcp_servers SET name=?,base_url=? WHERE id=? AND class_id=?",
			name, baseURL, id, classID); err != nil {
			if isDuplicate(err) {
				return ErrNameTaken
			}
			return err
		}
	}
	if changed {
		if _, err := tx.ExecContext(ctx, "DELETE FROM mcp_tools WHERE server_id=?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE mcp_servers SET status=?,last_error=NULL,connected_at=NULL WHERE id=?", StatusDisconnected, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Delete 删除服务器；工具缓存随外键级联清理。
func (s *Store) Delete(ctx context.Context, classID, id uint64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM mcp_servers WHERE id=? AND class_id=?", id, classID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Connect 完成握手与发现，并用本次结果整体替换缓存。
// 任一阶段失败都按"未连接 + 空清单 + 可读原因"落库，不保留上一次的成功结果。
func (s *Store) Connect(ctx context.Context, classID, id uint64) ([]Tool, error) {
	server, err := s.Get(ctx, classID, id)
	if err != nil {
		return nil, err
	}
	token, err := s.tokenOf(ctx, classID, id)
	if err != nil {
		return nil, err
	}
	client, err := NewClient(server.BaseURL, token, s.timeout)
	if err != nil {
		s.markFailed(ctx, id, "Base URL 无效")
		return nil, ErrInvalidURL
	}
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if _, err := client.Initialize(callCtx); err != nil {
		s.markFailed(ctx, id, describe(err))
		return nil, err
	}
	tools, err := client.Discover(callCtx)
	if err != nil {
		s.markFailed(ctx, id, describe(err))
		return nil, err
	}
	if err := s.replaceTools(ctx, classID, id, tools); err != nil {
		s.markFailed(ctx, id, "工具清单写入失败")
		return nil, err
	}
	return tools, nil
}

// Disconnect 清空缓存并回到未连接。
func (s *Store) Disconnect(ctx context.Context, classID, id uint64) error {
	if _, err := s.Get(ctx, classID, id); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM mcp_tools WHERE server_id=? AND class_id=?", id, classID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE mcp_servers SET status=?,last_error=NULL,connected_at=NULL WHERE id=? AND class_id=?",
		StatusDisconnected, id, classID); err != nil {
		return err
	}
	return tx.Commit()
}

// Tools 返回本班某台服务器最近一次发现的工具清单。
func (s *Store) Tools(ctx context.Context, classID, serverID uint64) ([]Tool, error) {
	if _, err := s.Get(ctx, classID, serverID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT name,description,input_schema FROM mcp_tools WHERE server_id=? AND class_id=? ORDER BY name",
		serverID, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tools := make([]Tool, 0, 4)
	for rows.Next() {
		var tool Tool
		var schema []byte
		if err := rows.Scan(&tool.Name, &tool.Description, &schema); err != nil {
			return nil, err
		}
		tool.InputSchema = json.RawMessage(schema)
		tools = append(tools, tool)
	}
	return tools, rows.Err()
}

// Available 把引用解析成当前可用的声明：只判"服务器已连接且工具在缓存里"。
// 配置期的四类校验不在这里重做——那是保存技能时的事。
func (s *Store) Available(ctx context.Context, classID uint64, refs []Ref) ([]Declaration, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id,s.name,t.name,t.description,t.input_schema
		 FROM mcp_tools t JOIN mcp_servers s ON s.id=t.server_id
		 WHERE t.class_id=? AND s.status=? ORDER BY s.id,t.name`, classID, StatusConnected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ tool, server string }
	wanted := make(map[key]struct{}, len(refs))
	order := make([]key, 0, len(refs))
	for _, ref := range refs {
		item := key{ref.Tool, ref.Server}
		if _, duplicated := wanted[item]; duplicated {
			continue
		}
		wanted[item] = struct{}{}
		order = append(order, item)
	}
	found := make(map[key]Declaration, len(order))
	for rows.Next() {
		var serverID uint64
		var serverName, toolName, description string
		var schema []byte
		if err := rows.Scan(&serverID, &serverName, &toolName, &description, &schema); err != nil {
			return nil, err
		}
		item := key{toolName, serverName}
		if _, ok := wanted[item]; !ok {
			continue
		}
		found[item] = Declaration{
			Ref: toolName + string(RefSeparator) + serverName, ServerID: serverID, ServerName: serverName,
			Name: toolName, Description: description, Parameters: json.RawMessage(schema),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	declarations := make([]Declaration, 0, len(order))
	for _, item := range order {
		if declaration, ok := found[item]; ok {
			declarations = append(declarations, declaration)
		}
	}
	return declarations, nil
}

// ValidateRefs 是保存技能时的四类校验：格式、属于本班、已连接、在清单内。
// 第一条不合规的引用即返回，错误文案指出具体引用与失败类别。
func (s *Store) ValidateRefs(ctx context.Context, classID uint64, refs []string) error {
	for _, raw := range refs {
		ref, err := ParseRef(raw)
		if err != nil {
			return err
		}
		var serverID uint64
		var status string
		err = s.db.QueryRowContext(ctx,
			"SELECT id,status FROM mcp_servers WHERE class_id=? AND name=?", classID, ref.Server).
			Scan(&serverID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return &RefError{Ref: ref.String(), Category: CategoryServer, Detail: "本班没有这台服务器"}
		}
		if err != nil {
			return err
		}
		if status != StatusConnected {
			return &RefError{Ref: ref.String(), Category: CategoryConnection, Detail: "服务器尚未连接，请先连接再保存"}
		}
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM mcp_tools WHERE server_id=? AND class_id=? AND name=?",
			serverID, classID, ref.Tool).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return &RefError{Ref: ref.String(), Category: CategoryCatalog, Detail: "该服务器最近一次发现结果里没有这个工具"}
		}
	}
	return nil
}

// Target 读取运行期调用所需的三项：地址、令牌、是否已连接。
// 未连接也算成功返回（connected=false），由调用方记为 denied 而不是报错。
func (s *Store) Target(ctx context.Context, classID, serverID uint64) (baseURL, token string, connected bool, err error) {
	var cipherText []byte
	var status string
	err = s.db.QueryRowContext(ctx,
		"SELECT base_url,auth_token_cipher,status FROM mcp_servers WHERE id=? AND class_id=?",
		serverID, classID).Scan(&baseURL, &cipherText, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, ErrNotFound
	}
	if err != nil {
		return "", "", false, err
	}
	if len(cipherText) > 0 {
		token, err = s.box.open(cipherText)
		if err != nil {
			return "", "", false, ErrSecret
		}
	}
	return baseURL, token, status == StatusConnected, nil
}

// NewClientFor 用服务器当前凭据构造客户端，供调用循环执行 tools/call。
func (s *Store) NewClientFor(baseURL, token string) (*Client, error) {
	return NewClient(baseURL, token, s.timeout)
}

func (s *Store) tokenOf(ctx context.Context, classID, serverID uint64) (string, error) {
	var cipherText []byte
	err := s.db.QueryRowContext(ctx,
		"SELECT auth_token_cipher FROM mcp_servers WHERE id=? AND class_id=?", serverID, classID).Scan(&cipherText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if len(cipherText) == 0 {
		return "", nil
	}
	return s.box.open(cipherText)
}

func (s *Store) markFailed(ctx context.Context, id uint64, reason string) {
	message := truncateRunes(reason, MaxErrorRunes)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM mcp_tools WHERE server_id=?", id); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE mcp_servers SET status=?,last_error=?,connected_at=NULL WHERE id=?",
		StatusError, message, id); err != nil {
		return
	}
	_ = tx.Commit()
}

func (s *Store) replaceTools(ctx context.Context, classID, serverID uint64, tools []Tool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 先删后插：本次结果就是全部事实，上一次多出来的工具不能残留。
	if _, err := tx.ExecContext(ctx, "DELETE FROM mcp_tools WHERE server_id=?", serverID); err != nil {
		return err
	}
	for _, tool := range tools {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO mcp_tools (class_id,server_id,name,description,input_schema) VALUES (?,?,?,?,?)",
			classID, serverID, tool.Name, truncateRunes(tool.Description, 1000), string(tool.InputSchema)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE mcp_servers SET status=?,last_error=NULL,connected_at=CURRENT_TIMESTAMP(6) WHERE id=?",
		StatusConnected, serverID); err != nil {
		return err
	}
	return tx.Commit()
}

func validateServer(name, baseURL string) error {
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("%w: 名称需为 1–%d 字", ErrInvalid, MaxNameRunes)
	}
	if baseURL == "" || len(baseURL) > MaxURLRunes {
		return fmt.Errorf("%w: Base URL 需为 1–%d 个字符", ErrInvalid, MaxURLRunes)
	}
	return nil
}

func describe(err error) string {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return "鉴权失败：令牌被拒绝"
	case errors.Is(err, ErrUpstream):
		return "调用失败：" + err.Error()
	}
	return truncateRunes(err.Error(), MaxErrorRunes)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func isDuplicate(err error) bool {
	var databaseError *mysql.MySQLError
	return errors.As(err, &databaseError) && databaseError.Number == 1062
}
