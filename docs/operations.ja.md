# 運用

[English](operations.md) · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · **日本語** · [Español](operations.es.md)

## サイジング

Mailhearth は、購入できる最小のホスト向けに作られています。

| リソース | 目安 | 備考 |
|---|---|---|
| バイナリ | 約 25 MB 静的 | cgo なし、ランタイム依存なし |
| アイドル時 RSS | 25–40 MB | `GOMEMLIMIT` の既定値は 160 MiB |
| ビジー時 RSS（10 ユーザー） | 60–120 MB | 大半は IMAP 取得バッファー |
| CPU | 無視できる | ログイン時の argon2id が最も重い処理 |
| ディスク | 数 MB | SQLite：組織モデルとコラボレーションのメタデータ。メールは Purelymail 上に残る |
| フロントエンド | gzip 後 51 KB | JS チャンク 1 個、CSS ファイル 1 個、イミュータブルキャッシュ |

`MAILHEARTH_IMAP_MAX_CONNS`（既定 24）は「同時利用ユーザー数 × 2 に、利用者が
開いたままにしているメールボックスの数を足した値」を目安に調整します。各 IDLE
ウォッチャーは 1 本の接続を保持します。Purelymail はユーザーごとにかなりの数の
IMAP 接続を許可しますが、必要以上に保持する理由はありません。

## バックアップ

状態のすべてはデータディレクトリです。

| パス | 内容 |
|---|---|
| `/data/mailhearth.db` | SQLite データベース（WAL モード） |
| `/data/mailhearth.db-wal` | 先行書き込みログ。データベースと一緒にコピーする |
| `/data/master.key` | 保存済み認証情報を暗号化する鍵 |
| `/data/uploads/` | 送信待ちの作成中添付ファイル（一時的） |

コンテナを停止してバックアップするか、`sqlite3 mailhearth.db ".backup
out.db"` で一貫性のあるオンラインコピーを取得します。`master.key` は別の安全な
場所に保管します。新しいホストでの復元は、両方のファイルを置いてバイナリを
起動するだけです。

## アップグレード

移行は起動時に自動で実行され、追加のみです。新しいイメージを取得し、
`docker compose up -d` を実行します。移行をまたぐダウングレードはサポートして
いません。代わりにバックアップから復元してください。

## リバースプロキシ

Caddy：

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx：`/api/mail/` に `proxy_buffering off;` と `proxy_read_timeout 3600s;` を
設定して SSE ストリームがバッファリングされないようにし、添付ファイル用に
`client_max_body_size 50m` を設定します。

## トラブルシューティング

**「Purelymail がこの API トークンを拒否しました」** — トークンが失効したか、
入力が誤っています。管理 → 接続 で差し替えてください。

**メールボックスが「未接続」と表示される** — インポートされたメールボックスに
アプリパスワードが付くのは、誰かが紐付けたとき（オンボーディング）か、管理者が
*接続* を押したときだけです。Purelymail が拒否する場合
（`createAppPassword` の失敗）、そのユーザーがまだアカウントに存在するかを
確認し、*今すぐ同期* を実行してください。

**「メールサーバーがこのメールボックスの認証情報を拒否しました」** — アプリ
パスワードが Purelymail のポータルで削除されたか、ユーザーのパスワードが
Mailhearth の外でリセットされています。そのメールボックスで *認証情報を
ローテーション* を使用してください。

**ルールを保存できない** — ホストから `mailserver.purelymail.com:4190` の
ManageSieve（STARTTLS）に到達できる必要があります。一部の VPS 事業者は
外向きポートを遮断しています。`openssl s_client -starttls sieve -connect
mailserver.purelymail.com:4190` でテストしてください。

**ライブ更新がない** — SSE がプロキシでバッファリングされています。上記を
参照してください。クライアントは手動更新に切り替わり、操作が起きたときには
引き続きフォルダーをポーリングします。

**巨大なフォルダーの最初のページが遅い** — 一覧表示は 50 通のエンベロープと
本文構造を取得する単一のシーケンス範囲 FETCH です。Purelymail のサーバー側
検索インデックス（ユーザー単位で有効化）により検索は高速になります。
Mailhearth が作成するメールボックスでは有効になっています。

## ログ

構造化テキストログを stderr に出力します。`MAILHEARTH_LOG_LEVEL=debug` を
設定すると、リクエストごとの行と IMAP ウォッチャーの再接続が追加されます。
ログに認証情報は含まれません。

## 開発環境のフェイク実装

`MAILHEARTH_DEV_STACK=1` は Purelymail、IMAP、SMTP をプロセス内のフェイクに
差し替えます。`-seed-demo` は 2 つのドメイン、5 人のユーザー、ルーティング
ルール、サンプルメールを追加します。フェイクの API トークンは `dev-token` で、
ユーザーは `<name>-pass` で認証します。Go のテストも同じフェイクを使うため、
`go test ./...` は IMAP IDLE、SMTP 送信、HTML サニタイズを含む経路全体を
通します。
