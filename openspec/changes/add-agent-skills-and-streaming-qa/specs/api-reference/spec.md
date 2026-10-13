# Spec Delta

## ADDED Requirements

### Requirement: An OpenAPI description is served with a browsable reference page

系统 SHALL 提供本服务的 OpenAPI 描述，并在浏览器可访问的路径上挂载 Swagger 页面，供对照文档试调主要接口。文档 MUST 覆盖本课新增的主要接口（智能体、技能、会话、消息与流式问答）与既有的认证、材料、检索接口，MUST 与实现的路径、方法、请求体与响应字段保持一致；文档路径 MUST 在未登录时也可打开（试调时仍需有效凭据）。文档 MUST NOT 包含任何密钥、令牌或内部路径。

#### Scenario: The reference page is reachable

- **WHEN** 在浏览器里打开文档路径
- **THEN** 能看到可交互的接口清单，按分组列出各接口的路径、方法与说明

#### Scenario: The description matches the implementation

- **WHEN** 按文档对一个受保护接口发起试调并带上有效凭据
- **THEN** 请求成功，返回体字段与文档描述一致

#### Scenario: The description leaks no credentials

- **WHEN** 检查文档内容
- **THEN** 其中不含任何密钥、令牌、账号口令或内部文件路径
