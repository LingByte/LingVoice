package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	url := "ws://localhost:9090/ws/voice"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}

	fmt.Println("连接:", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatal("连接失败:", err)
	}
	defer conn.Close()

	// 1. 发送 Offer
	offer := `{"type":"offer","version":1,"media":{"audio":{"codecs":["opus","pcmu"],"sampleRates":[48000,8000],"channels":[1]}}}`
	fmt.Println("→ Offer:", offer)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(offer)); err != nil {
		log.Fatal("发送 offer 失败:", err)
	}

	// 2. 等待 Answer
	_, data, err := conn.ReadMessage()
	if err != nil {
		log.Fatal("读取 answer 失败:", err)
	}
	fmt.Println("← Answer:", string(data))

	// 3. 发送 Start
	start := `{"type":"start"}`
	fmt.Println("→ Start:", start)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(start)); err != nil {
		log.Fatal("发送 start 失败:", err)
	}

	// 4. 等待 Ready
	_, data, err = conn.ReadMessage()
	if err != nil {
		log.Fatal("读取 ready 失败:", err)
	}
	fmt.Println("← Ready:", string(data))

	// 5. 发送一个二进制音频帧
	frame := make([]byte, 12) // 8 header + 4 payload
	frame[0] = 0x01            // audio
	frame[1] = 0x01            // opus
	binary.BigEndian.PutUint16(frame[2:4], 1)   // sequence
	binary.BigEndian.PutUint32(frame[4:8], 960) // timestamp (20ms @ 48kHz)
	frame[8] = 0x01
	frame[9] = 0x02
	frame[10] = 0x03
	frame[11] = 0x04
	fmt.Printf("→ Binary frame: % x\n", frame)
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		log.Fatal("发送 binary 失败:", err)
	}

	time.Sleep(500 * time.Millisecond)
	fmt.Println("✓ 协议测试完成")
}
