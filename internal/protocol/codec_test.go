package protocol

import (
	"bytes"   // 提供 bytes.Buffer / bytes.NewReader,可当内存里的 io.Reader/Writer 用
	"errors"  // errors.Is 用来判断错误类型
	"testing" // Go 内置测试包
)

// TestEncodeReadFrameRoundTrip 验证"往返一致":Encode 打包后再 ReadFrame 解包,
// 必须能原样还原出 msgType 和 body。这是协议最核心的正确性保证。
//
// 用表驱动(table-driven)写法:把多组输入放进一个切片,循环逐个跑。
// 这是 Go 社区最推崇的测试组织方式 —— 加用例只要加一行,不用复制粘贴一整个函数。
func TestEncodeReadFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string // 子测试名,失败时能一眼看出是哪组挂了
		typ  uint16
		body []byte
	}{
		{"normal", 42, []byte("hello world")},
		{"empty body", 7, []byte{}},                           // 空消息体也要能处理
		{"binary body", 1000, []byte{0x00, 0xFF, 0x0A, 0xAB}}, // 含 0x00 和 0x0A(\n),验证二进制安全
		{"max type", 65535, []byte("x")},                      // uint16 上界
	}

	for _, c := range cases {
		// t.Run 开一个"子测试",每组用例独立报告成功/失败。
		t.Run(c.name, func(t *testing.T) {
			packet, err := Encode(c.typ, c.body)
			if err != nil {
				t.Fatalf("Encode 失败: %v", err)
			}

			// bytes.NewReader 把字节切片包成一个 io.Reader,直接喂给 ReadFrame。
			// 这就是 ReadFrame 用 io.Reader 接口的好处:测试里不需要真的 TCP 连接。
			gotType, gotBody, err := ReadFrame(bytes.NewReader(packet))
			if err != nil {
				t.Fatalf("ReadFrame 失败: %v", err)
			}

			if gotType != c.typ {
				t.Errorf("类型不一致: got %d, want %d", gotType, c.typ)
			}
			// 切片不能用 == 比较,要用 bytes.Equal。
			if !bytes.Equal(gotBody, c.body) {
				t.Errorf("消息体不一致: got %q, want %q", gotBody, c.body)
			}
		})
	}
}

// TestReadFrameHandlesStickyPackets 是本文件的灵魂测试:验证粘包处理。
// 把三条消息的字节全部拼进一个 buffer(模拟 TCP 把它们粘成一坨),
// 然后 ReadFrame 应该能一条一条精确切出来,互不串味。
func TestReadFrameHandlesStickyPackets(t *testing.T) {
	messages := []struct {
		typ  uint16
		body []byte
	}{
		{1, []byte("first")},
		{2, []byte("second-message-is-longer")},
		{3, []byte{}}, // 中间夹一条空消息,边界更容易暴露 bug
		{4, []byte("ssa")},
	}

	// 把所有消息打包后写进同一个 stream —— 这就是"粘在一起"的样子。
	var stream bytes.Buffer
	for _, m := range messages {
		packet, err := Encode(m.typ, m.body)
		if err != nil {
			t.Fatalf("Encode(type=%d) 失败: %v", m.typ, err)
		}
		stream.Write(packet)
	}

	// 从粘在一起的流里,一条条读回来,顺序和内容都要对得上。
	for i, want := range messages {
		gotType, gotBody, err := ReadFrame(&stream)
		if err != nil {
			t.Fatalf("第 %d 条 ReadFrame 失败: %v", i, err)
		}
		if gotType != want.typ {
			t.Errorf("第 %d 条类型: got %d, want %d", i, gotType, want.typ)
		}
		if !bytes.Equal(gotBody, want.body) {
			t.Errorf("第 %d 条消息体: got %q, want %q", i, gotBody, want.body)
		}
	}
}

// TestEncodeRejectsOversizedBody 验证安全上限:body 过大时 Encode 必须报错,
// 而不是放任打包出一个超大包。
func TestEncodeRejectsOversizedBody(t *testing.T) {
	// length = TypeFieldSize + len(body) = 2 + MaxPacketSize,必然 > MaxPacketSize。
	body := make([]byte, MaxPacketSize)

	_, err := Encode(1, body)
	// errors.Is 沿着错误链查找:因为 Encode 用 %w 包装了 ErrPacketTooLarge,这里能匹配上。
	if !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("got err %v, want ErrPacketTooLarge", err)
	}
}

// TestReadFrameRejectsTooSmallLength 验证:长度字段比一个类型字段还小,属于非法包,要拒绝。
func TestReadFrameRejectsTooSmallLength(t *testing.T) {
	// 手工构造非法包:长度字段大端写成 1(< TypeFieldSize=2)。
	// ReadFrame 读完这 4 字节就该判定非法,根本不会去读 body。
	illegal := []byte{0, 0, 0, 1}

	_, _, err := ReadFrame(bytes.NewReader(illegal))
	if !errors.Is(err, ErrPacketTooSmall) {
		t.Fatalf("got err %v, want ErrPacketTooSmall", err)
	}
}
