package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"myobj/src/config"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
)

var LOG *slog.Logger

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[37m"
	colorBold   = "\033[1m"
)

// MultiHandler 自定义 Handler：同时输出到控制台和文件
type MultiHandler struct {
	consoleHandler slog.Handler
	fileHandler    slog.Handler
}

func NewMultiHandler(consoleHandler, fileHandler slog.Handler) *MultiHandler {
	return &MultiHandler{
		consoleHandler: consoleHandler,
		fileHandler:    fileHandler,
	}
}

func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.consoleHandler.Enabled(ctx, level) || h.fileHandler.Enabled(ctx, level)
}

// 自定义控制台输出
func (h *MultiHandler) writeConsole(r slog.Record) error {
	// 格式：时间 + 级别 + JSON
	timeStr := r.Time.Format("[2006-01-02 15:04:05]")
	levelStr := r.Level.String()
	// 根据日志级别添加颜色
	switch r.Level {
	case slog.LevelError:
		levelStr = colorBold + colorRed + "ERROR" + colorReset
	case slog.LevelWarn:
		levelStr = colorYellow + "WARN" + colorReset
	case slog.LevelInfo:
		levelStr = colorBlue + "INFO" + colorReset
	case slog.LevelDebug:
		levelStr = colorGray + "DEBUG" + colorReset
	default:
		levelStr = r.Level.String()
	}

	// 收集所有属性
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})

	// 构建 JSON，确保msg在最前面
	var jsonStr string
	msgJSON, _ := json.Marshal(r.Message)
	if len(attrs) > 0 {
		// 先放 msg
		jsonStr = fmt.Sprintf(`{"msg":%s`, string(msgJSON))
		// 再放其他属性
		for key, value := range attrs {
			valueJSON, _ := json.Marshal(value)
			jsonStr += fmt.Sprintf(`,"%s":%s`, key, string(valueJSON))
		}
		jsonStr += "}"
	} else {
		jsonStr = fmt.Sprintf(`{"msg":%s}`, string(msgJSON))
	}

	// 直接写入 os.Stdout
	_, err := fmt.Fprintf(os.Stdout, "%s %s %s\n", timeStr, levelStr, jsonStr)
	return err
}

func (h *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	// 仅对 Info 及以上级别添加源代码位置
	if r.Level != slog.LevelInfo {
		// 获取调用栈信息 (跳过3层：runtime.Callers -> 此方法 -> slog记录点)
		var pcs [1]uintptr
		runtime.Callers(4, pcs[:])
		frames := runtime.CallersFrames(pcs[:])

		frame, _ := frames.Next()
		if frame.PC != 0 {
			// 提取简洁的文件名和函数名
			file := filepath.Base(frame.File)
			function := shortenFuncName(frame.Function)

			// 添加到日志属性
			r.AddAttrs(
				slog.String("source", file),
				slog.Int("line", frame.Line),
				slog.String("func", function),
			)
		}
	}
	// err1 := h.consoleHandler.Handle(ctx, r)
	err1 := h.writeConsole(r)
	err2 := h.fileHandler.Handle(ctx, r)
	if err1 != nil {
		return err1
	}
	return err2
}

// 简化函数名 (去掉包路径)
func shortenFuncName(f string) string {
	// 去掉包路径前缀
	if idx := strings.LastIndex(f, "/"); idx != -1 {
		f = f[idx+1:]
	}

	// 去掉类型接收器部分
	if idx := strings.Index(f, "."); idx != -1 {
		return f[idx+1:]
	}
	return f
}

func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return NewMultiHandler(
		h.consoleHandler.WithAttrs(attrs),
		h.fileHandler.WithAttrs(attrs),
	)
}

func (h *MultiHandler) WithGroup(name string) slog.Handler {
	return NewMultiHandler(
		h.consoleHandler.WithGroup(name),
		h.fileHandler.WithGroup(name),
	)
}

// InitLogger 初始化日志系统
func InitLogger() {
	cfg := config.CONFIG.Log
	// 确保日志目录存在
	if err := os.MkdirAll(cfg.LogPath, 0750); err != nil {
		log.Fatalf("创建日志目录失败: %v", err)
	}

	// 配置日志级别
	logLevel := new(slog.LevelVar)
	switch cfg.Level {
	case "debug":
		logLevel.Set(slog.LevelDebug)
	case "warn":
		logLevel.Set(slog.LevelWarn)
	case "error":
		logLevel.Set(slog.LevelError)
	default:
		logLevel.Set(slog.LevelInfo)
	}

	// 配置日志轮转规则
	rotationLog, err := rotatelogs.New(
		cfg.LogPath+"app.%Y%m%d.log",                                  // 按日期分片
		rotatelogs.WithRotationTime(24*time.Hour),                     // 每天轮转
		rotatelogs.WithMaxAge(time.Duration(cfg.MaxAge)*24*time.Hour), // 保留天数
		rotatelogs.WithRotationSize(int64(cfg.MaxSize)*1024*1024),     // 按大小分片
		//rotatelogs.WithRotationCount(uint(cfg.MaxBackups)),            // 保留文件数
	)
	if err != nil {
		log.Fatalf("创建日志轮转器失败: %v", err)
	}

	// 创建控制台处理器（文本格式）
	consoleHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// 自定义时间格式
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	})

	// 创建文件处理器（JSON格式）
	fileHandler := slog.NewJSONHandler(rotationLog, &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// 自定义时间格式
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	})

	// 创建组合处理器
	multiHandler := NewMultiHandler(consoleHandler, fileHandler)

	// 创建日志器
	logger := slog.New(multiHandler)
	slog.SetDefault(logger)
	LOG = logger
	LOG.Info("日志系统初始化完成🧩🧩🧩🧩🧩🧩")
}
