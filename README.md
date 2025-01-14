# HTTPS Proxy Server

高性能なHTTPSプロキシサーバー。アクセス制御、パフォーマンス最適化、詳細なモニタリング機能を提供します。

## 機能

- **HTTPSトンネリング**
    - CONNECTメソッドのサポート
    - 効率的な接続プール管理
    - Keep-Alive接続

- **アクセス制御**
    - IPベースのブロッキング
    - ドメインベースのブロッキング（ワイルドカードサポート）
    - YAMLベースの設定

- **パフォーマンス最適化**
    - 接続プーリング
    - スレッドセーフな実装
    - 自動的な接続クリーンアップ

- **モニタリング**
    - Prometheusメトリクス
    - JSON形式の詳細統計
    - ヘルスチェックエンドポイント

- **ロギング**
    - 構造化ログ
    - 自動ログローテーション
    - エラー追跡

## クイックスタート

### インストール

```bash
git clone https://github.com/yourusername/proxy.git
cd proxy
go build -o proxy cmd/proxy/main.go
```

### 基本的な使用方法

1. プロキシサーバーを起動:
```bash
./proxy
```

2. curlでテスト:
```bash
curl -v --proxy http://localhost:10080 https://example.com
```

3. メトリクスの確認:
```bash
curl http://localhost:10081/metrics
```

## 設定

### コマンドライン引数

| オプション | デフォルト値 | 説明 |
|------------|--------------|------|
| -port | 10080 | プロキシサーバーのポート |
| -metrics-port | 10081 | メトリクスサーバーのポート |
| -config-dir | ./configs | 設定ディレクトリ |
| -log-dir | ./logs | ログディレクトリ |
| -cache-dir | ./cache | キャッシュディレクトリ |
| -max-cache-size | 104857600 | キャッシュ最大サイズ（バイト） |
| -max-connections | 1000 | 最大同時接続数 |

### アクセス制御設定

`configs/blocked.yaml`:
```yaml
blocked_ips:
  - 192.168.1.100
  - 10.0.0.50

blocked_domains:
  - example.com
  - "*.malicious.com"
```

## ユースケース

### 開発環境での使用

```bash
# カスタムポートで起動
./proxy -port 8080 -metrics-port 8081

# APIリクエストのテスト
curl -v --proxy http://localhost:8080 https://api.example.com
```

### トラフィック監視

```bash
# 詳細な監視設定で起動
./proxy -max-connections 2000

# 統計情報の確認
curl http://localhost:10081/stats | jq
```

### 大規模環境での運用

```bash
# キャッシュとコネクション設定を調整
./proxy -max-cache-size 524288000 -max-connections 5000
```

## API エンドポイント

### メトリクス

```
GET http://localhost:{metrics-port}/metrics
```
Prometheusフォーマットのメトリクスを提供

### 統計情報

```
GET http://localhost:{metrics-port}/stats
```
詳細な統計情報をJSON形式で提供

### ヘルスチェック

```
GET http://localhost:{metrics-port}/health
```
サーバーの状態を確認

## プロジェクト構造

```
.
├── cmd/
│   └── proxy/
│       └── main.go           # エントリーポイント
├── internal/
│   ├── domain/              # ドメインモデル
│   ├── usecase/             # ユースケース
│   └── interface/           # インターフェース
├── configs/
│   └── blocked.yaml         # アクセス制御設定
├── logs/                    # ログファイル（自動作成）
└── cache/                   # キャッシュ（自動作成）
```
