# Markdown 内容验证

支持 **粗体**、*斜体*、~~删除线~~ 和 `inline code`，以及 [QuickGUI](https://github.com/egoist/quickgui)。

## 列表与引用

1. 第一项：中文和 English 混排。
2. 第二项包含 **重点内容**。
   - 嵌套列表

> 引用中的文字保持清晰，长段落会根据窗口宽度自动换行。

- [x] 已完成 Markdown 渲染
- [ ] 后续工作

```go
func main() {
    fmt.Println("Hello, 世界")
}
```

| 类型 | 状态 |
| --- | --- |
| 普通消息 | 支持 |
| 流式输出 | 支持 |

---

转义：\*不是斜体\* &amp; 中文。  
这一行是硬换行。
