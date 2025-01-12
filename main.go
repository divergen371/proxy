// Package main はシンプルなHTTPSプロキシサーバーを実装します
// このプロキシサーバーは、HTTPSトンネリング（CONNECT メソッド）をサポートし、
// クライアントとサーバー間の安全な通信を仲介します
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
)

// main はプロキシサーバーを起動し、クライアントからの接続を待ち受けます
// ポート10080でTCP接続をリッスンし、新しい接続ごとにゴルーチンを起動して
// 並行処理を実現します
func main() {
	listener, err := net.Listen(("tcp"), ":10080")
	if err != nil {
		fmt.Println("Error starting server:", err)
		return
	}
	defer listener.Close()
	fmt.Println("TCP server listening on port 10080:", err)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			return
		}

		go handleConnection(conn)
	}
}

// handleConnection はクライアントとの接続を処理します
// HTTPS CONNECTトンネリングをサポートし、以下の手順で処理を行います：
// 1. クライアントからのCONNECTリクエストを読み取り
// 2. 対象サーバーへの接続を確立
// 3. クライアントへ200 OKレスポンスを送信
// 4. クライアントとサーバー間の双方向トンネリングを作成
//
// トンネリングの仕組み：
// - 2つのゴルーチンを使用して双方向データ転送を実現
// - isClosed チャネルを使用して接続終了を検知
// - io.Copy を使用して効率的なデータ転送を実現
//
// エラーハンドリング：
// - 接続エラー、リクエスト読み取りエラー、レスポンス送信エラーを適切に処理
// - エラー発生時はログ出力して接続を終了
//
// パラメータ：
//   - clientConn: クライアントからのTCP接続
func handleConnection(clientConn net.Conn) {
	defer func(clientConn net.Conn) {
		err := clientConn.Close()
		if err != nil {
			fmt.Println("Error closing client connection:", err)
		}
	}(clientConn)
	fmt.Println("Client Connected:", clientConn.RemoteAddr().String())

	// クライアントからのHTTPリクエストを読み取り
	reader := bufio.NewReader(clientConn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		fmt.Println("Error reading request:", err)
		return
	}

	fmt.Println("Request received!")
	switch req.Method {
	case http.MethodConnect:
		// HTTPS CONNECTトンネリングの処理
		fmt.Println("Connect to server:", req.URL.Host)
		serverConn, err := net.Dial("tcp", req.URL.Host)
		if err != nil {
			fmt.Println("Error connecting to server:", err)
			return
		}
		defer func(serverConn net.Conn) {
			err := serverConn.Close()
			if err != nil {
				fmt.Println("Error closing server connection:", err)
			}
		}(serverConn)

		// クライアントへ接続確立応答を送信
		response := &http.Response{
			StatusCode: http.StatusOK,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
		}

		if err := response.Write(clientConn); err != nil {
			fmt.Println("Error writng response:", err)
			return
		}

		// 双方向トンネルの作成
		isClosed := make(chan bool, 2)

		// クライアント → サーバーの転送
		go func() {
			_, err := io.Copy(serverConn, clientConn)
			if err != nil {
				fmt.Println("Error copying data from client to server:", err)
			}
			isClosed <- true
		}()

		// サーバー → クライアントの転送
		go func() {
			_, err := io.Copy(clientConn, serverConn)
			if err != nil {
				fmt.Println("Error copying data from server to client:", err)
			}
			isClosed <- true
		}()

		<-isClosed

	default:
		fmt.Println("Method not supported:", req.Method)
	}
}
