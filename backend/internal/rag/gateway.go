package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrDependency 表示外部依赖（嵌入/对话网关或向量库）不可用。
// 调用方必须据此返回 503，而不是把依赖故障伪装成"资料里没有"。
var ErrDependency = errors.New("rag dependency unavailable")

// Gateway 是 OpenAI 兼容的课程模型网关客户端。密钥只存在于服务端进程环境。
// 两个客户端分开持有超时：普通调用有总超时，流式调用交给请求上下文（可能生成很久）。
type Gateway struct {
	config Config
	client *http.Client
	stream *http.Client
}

func newGateway(config Config) *Gateway {
	return &Gateway{
		config: config,
		client: &http.Client{Timeout: 60 * time.Second},
		stream: &http.Client{},
	}
}

// VectorEnabled 表示嵌入配置齐全，可以使用向量路径。
func (g *Gateway) VectorEnabled() bool {
	if g == nil {
		return false
	}
	return g.config.EmbeddingBaseURL != "" && g.config.EmbeddingModel != "" &&
		g.config.EmbeddingAPIKey != "" && g.config.EmbeddingDimension > 0
}

// ChatEnabled 表示对话配置齐全，可以生成回答。
func (g *Gateway) ChatEnabled() bool {
	if g == nil {
		return false
	}
	return g.config.ChatBaseURL != "" && g.config.ChatModel != ""
}

type embeddingReply struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed 调用嵌入网关。返回的每个向量长度必须等于配置的 EMBEDDING_DIM，
// 否则视为失败——写入维度不一致的向量会让后续检索永久错位。
func (g *Gateway) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !g.VectorEnabled() || len(texts) == 0 {
		return nil, ErrDependency
	}
	body, err := json.Marshal(map[string]any{"model": g.config.EmbeddingModel, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("%w: encode request", ErrDependency)
	}
	endpoint := strings.TrimRight(g.config.EmbeddingBaseURL, "/") + "/embeddings"
	var reply embeddingReply
	if err := g.postJSON(ctx, endpoint, g.config.EmbeddingAPIKey, body, &reply); err != nil {
		return nil, err
	}
	if len(reply.Data) != len(texts) {
		return nil, fmt.Errorf("%w: embedding count mismatch", ErrDependency)
	}
	vectors := make([][]float32, len(reply.Data))
	for index, item := range reply.Data {
		if len(item.Embedding) != g.config.EmbeddingDimension {
			return nil, fmt.Errorf("%w: embedding dimension mismatch", ErrDependency)
		}
		vectors[index] = item.Embedding
	}
	return vectors, nil
}

type chatReply struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Answer 只把命中的切片交给对话网关，并要求用 [n] 标注出处。
func (g *Gateway) Answer(ctx context.Context, question string, hits []Hit) (string, error) {
	if !g.ChatEnabled() {
		return "", ErrDependency
	}
	var context strings.Builder
	for index, hit := range hits {
		fmt.Fprintf(&context, "[%d] 来源：%s（切片 #%d，偏移 %d–%d）\n%s\n\n",
			index+1, hit.Title, hit.ChunkIndex, hit.StartOffset, hit.EndOffset, hit.Text)
	}
	system := "你是班级资料室的问答助手。只能依据用户给出的资料片段回答，不要使用片段以外的知识，也不要编造。" +
		"回答要简短直接，一般只用一两句话。每个结论后面用 [序号] 标出它来自哪个片段，序号必须与片段编号一致。"
	user := "资料片段：\n\n" + context.String() + "问题：" + question + "\n\n请只依据以上片段作答，并标出 [序号]。"
	payload, err := json.Marshal(map[string]any{
		"model": g.config.ChatModel,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.2,
		"max_tokens":  400,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode request", ErrDependency)
	}
	endpoint := strings.TrimRight(g.config.ChatBaseURL, "/") + "/chat/completions"
	var reply chatReply
	if err := g.postJSON(ctx, endpoint, g.config.ChatAPIKey, payload, &reply); err != nil {
		return "", err
	}
	if len(reply.Choices) == 0 {
		return "", fmt.Errorf("%w: empty completion", ErrDependency)
	}
	answer := strings.TrimSpace(reply.Choices[0].Message.Content)
	if answer == "" {
		return "", fmt.Errorf("%w: blank completion", ErrDependency)
	}
	return answer, nil
}

// postJSON 发送 JSON 请求并解码响应；任何非 2xx、超时或解码失败都归为依赖不可用。
func (g *Gateway) postJSON(ctx context.Context, endpoint, apiKey string, body []byte, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build request", ErrDependency)
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := g.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDependency, err)
	}
	defer response.Body.Close()
	// 只读有限长度，避免异常上游把响应体撑爆内存；错误体不进入日志，避免带出密钥。
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: read response", ErrDependency)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%w: status %d", ErrDependency, response.StatusCode)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("%w: decode response", ErrDependency)
	}
	return nil
}
