# システム運用・保守ガイド

[English](operations.md) · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · **日本語** · [Español](operations.es.md)

## サイジングとリソース消費

Mailhearth は極めて小さなフットプリントで動作するように設計されており、市販の最も低価格なエントリークラス VPS インスタンス上でも軽快に動作します。

| リソース項目 | 典型的な消費量 | 運用上の留意事項 |
|---|---|---|
| 実行可能バイナリ | 約 25 MB（静的） | cgo 依存ゼロ。ランタイムライブラリや外部 VM は不要 |
| アイドル時常駐メモリ (RSS) | 25〜40 MB | Go のソフトメモリ制限 `GOMEMLIMIT=160MiB` を標準適用し突発的な消費を抑制 |
| ビジー時常駐メモリ（10 人同時利用） | 60〜120 MB | メモリの大半は IMAP シーケンスの一括取得および MIME 解析バッファで消費 |
| CPU 負荷 | 通常時はほぼ 0% | 最も計算負荷が高い処理はログイン時の argon2id パスワードハッシュ計算のみ |
| ディスク容量の推移 | 組織メタデータの増加にのみ比例 | SQLite には組織、権限、監査履歴、社内メモを保存。RFC 822 メール本文はサーバー側に保持 |
| Web フロントエンド容量 | gzip 後 約 100 KB | Web メールと管理画面を完全内包。ファイル名にハッシュを含むため長期キャッシュが可能 |

### IMAP コネクションプールのチューニング

環境変数 `MAILHEARTH_IMAP_MAX_CONNS`（既定値: 24）により、システム全体の同時接続プールを調整します。目安となる計算式：`予想同時アクティブユーザー数 × 2 + ブラウザーで開き放しにされるメールボックスの総数`。

コネクションプールは厳格な階層型クォータ制限を実施しています：
- **システム全体の最大接続数**：既定 24 接続；
- **接続ごとの上限**：単一のメールプロバイダー接続あたり最大 8 本の同時接続；
- **メールボックスごとの上限**：単一のメールボックスあたり最大 3 本の同時接続；
- **IDLE ウォッチャーの確保枠**：バックグラウンド IDLE プッシュリスナーは独立した予約枠で動作し、通常のリクエスト用に全体で 4 スロット、接続ごとに 2 スロットが常に確保されます。

> **注意**：接続数を増やす際は、上流メールプロバイダー（Purelymail や Migadu など）が設けている IP アドレス単位またはアカウント単位の接続レート制限を必ずご確認ください。

## バックアップとディザスタリカバリ

Mailhearth のすべての実行時ステートは、永続化ボリューム（既定 `/data`）の内部に格納されています：

| ファイル／ディレクトリ | 役割と説明 | バックアップ要件 |
|---|---|---|
| `/data/mailhearth.db` | システムのコア SQLite データベース（WAL モードで動作） | 中核データ |
| `/data/mailhearth.db-wal` | 直近のコミット済みトランザクションを保持する先行書き込みログ | 必ず `.db` とセットでバックアップ |
| `/data/master.key` | API トークンやプロトコルパスワードを暗号化・復号するためのマスターキー | 最重要機密。安全な外部ストレージに保管 |
| `/data/uploads/` | メール作成時に一時ステージングされる送信待ち添付ファイル | 一時ファイル。再起動時に自動再作成可能 |

### オンラインホットバックアップ
サーバーを停止することなく、SQLite のオンラインバックアップ機能を利用して一貫性のあるスナップショットを取得できます：
```bash
docker compose exec mailhearth sqlite3 /data/mailhearth.db ".backup /data/backup_$(date +%Y%m%d_%H%M%S).db"
```

### コールドバックアップ
コンテナを停止してデータボリュームを直接アーカイブします：
```bash
docker compose stop
tar -czvf mailhearth-backup-$(date +%Y%m%d).tar.gz /data/mailhearth.db /data/mailhearth.db-wal /data/master.key
docker compose start
```

### 新しいサーバーへのリカバリ手順
新しいホストへの復元は以下の簡単なステップで完了します：
1. 新ホストに Docker Compose 環境を用意します；
2. バックアップした `mailhearth.db`（ホットバックアップの場合はファイル名を `mailhearth.db` に変更）と `master.key` をホスト側のデータディレクトリに配置します；
3. `docker compose up -d` を実行します。起動時にマスターキーの検証と必要なスキーマチェックが自動実行され、シームレスに運用が再開されます。

## システムアップグレードとスキーママイグレーション

Mailhearth は組み込みのデータベースマイグレーションエンジン（`internal/db/migrations`）を備えています。

1. **自動マイグレーション**：新しいバイナリが起動すると、現在のスキーマバージョンを検知し、テーブル構造の更新、外部キー制約、整合性チェックを単一のトランザクション内で自動適用します。
2. **アップグレード前の安全策**：新しいコンテナイメージを取得する前に、必ず上記のバックアップ手順に従ってデータベースの完全なスナップショットと `master.key` のバックアップを保存してください。
3. **アップグレードの適用**：
   ```bash
   git pull
   docker compose pull
   docker compose up -d --build
   ```
4. **ロールバックに関する方針**：データベースマイグレーションは前方互換のみを前提としています。万が一以前のバージョンに切り戻す必要がある場合、**新しいスキーマに対して古いバイナリを実行することはサポートされていません**。古いイメージに戻すと同時に、アップグレード直前に取得したデータベースバックアップを復元してください。

## リバースプロキシと TLS 設定

本番運用では、公開アクセスの受付と TLS 証明書の自動終端を行うリバースプロキシ（Caddy または Nginx）の背後に Mailhearth を配置してください。

### Caddy（推奨）
Caddy を使用すると、Let's Encrypt 等の証明書を自動取得・更新でき、最小限の記述で稼働します：
```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        # Server-Sent Events (SSE) のリアルタイム配信を妨げないようバッファリングを即時フラッシュ
        flush_interval -1
    }
}
```

### Nginx
Nginx を使用する場合は、ストリーミングエンドポイントでのプロキシバッファリングを無効化し、適切なタイムアウトを設定してください：
```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    # 大きな添付ファイルのアップロードを許可
    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # リアルタイムプッシュ通信のためバッファリングを無効化
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

また、監査ログに訪問元の正確な IP アドレスを記録するため、`.env` に `MAILHEARTH_TRUST_PROXY=true` を設定してください。

## トラブルシューティングとインシデント対応

### 1. プロバイダー管理接続のエラー: `provider_auth_failed`
- **原因**: 外部プロバイダーの API トークンが期限切れ、失効、または必要なアクセス権限を満たしていません。
- **対応**: 「管理コンソール → メール接続」を開き、対象の接続の設定を編集して新しい管理 API トークンを入力してください。新しいトークンによる検証が成功するまで、既存の有効な設定が安全に保持されます。

### 2. メールボックスに「プロトコルが未設定」と表示される
- **原因**: リソースの初回取り込みではリモートのメタデータのみを取得しており、パスワードは自動取り込みされません。
- **対応**: 対象メールボックスの設定画面を開き、IMAP、SMTP、ManageSieve の各エンドポイントに必要な認証情報を設定してください。保存前に実際の認証テストが行われ、成功した場合にのみ原子的に保存されます。不要なプロトコルは無効化して構いません。

### 3. メールプロトコルの認証エラー: `mailbox_auth_failed`
- **原因**: 設定されたユーザー名、パスワード、またはアプリ専用パスワードがサーバーに拒否されました。
- **対応**: 手動入力（`entered`）の場合は設定画面からパスワードを更新してください。プラットフォーム管理（`managed`）の場合は管理コンソールから認証情報ローテーションを実行してください。

### 4. メールフィルタールールが保存できない
- **原因**: ManageSieve エンドポイントの設定誤り、TLS 通信エラー、またはサーバー側に必要な Sieve 拡張機能が存在しません。
- **対応**:
  - Purelymail の既定値は `mailserver.purelymail.com:4190`（STARTTLS）です；
  - Migadu の ManageSieve は初期状態で無効化されています。利用する場合は有効化して正しい認証情報を入力してください；
  - サーバー上に既存のアクティブスクリプトが存在する場合、予期せぬ上書きを防ぐため確認ダイアログが表示されます。明示的に引き継ぎを確認してください；
  - 不在時自動返信を利用するには、サーバーが `vacation` 拡張に対応している必要があります。

### 5. 非同期タスク（Operation）が `unknown` 状態になる
- **原因**: 外部プロバイダーへのデータ書き込み中にネットワーク障害やタイムアウトが発生し、リモートリソースの作成状況が未確定です。
- **対応**:
  - 当該タスクのステップログを確認してください。状態の更なる不整合を防ぐためリソース排他ロックは維持されます；
  - 認証情報作成中に通信が途絶えリモート ID が未取得となった場合、プロバイダーの管理画面で未紐付けのアプリ専用パスワードが存在しないか確認し、削除してください；
  - 確認後、画面上で「クリーンアップ報告を提出」を実行すると、タスクは安全にキャンセルされ（`external_reported`、`systemVerified: false`）、作成済みの他リソースを保持したまま正常に復帰します。

### 6. 送信ステータスが `sent_copy_failed` になる
- **原因**: メールは SMTP サーバーに正常に受け付けられ配信キューに入りましたが、送信済みフォルダー（Sent）への IMAP 保存時に通信障害が発生しました。
- **対応**: 宛先への二重送信の心配はありません！Mailhearth は送信とコピー保存を厳格に分離しています。「コピーの再保存」をクリックすると、IMAP の追加保存のみが再実行され、SMTP への再送信は一切行われません。

### 7. リアルタイム更新が届かず手動更新が必要になる
- **原因**: リバースプロキシが Server-Sent Events (SSE) の HTTP ストリームをバッファリングしています。
- **対応**: リバースプロキシの設定を見直し、Caddy の `flush_interval -1` または Nginx の `/api/mail/` に対する `proxy_buffering off` が適用されていることを確認してください。

### 8. 巨大なフォルダーの初回表示に時間がかかる
- **説明**: フォルダー展開時、Mailhearth は 50 件のメッセージエンベロープと MIME 構造を単一のシーケンス FETCH で一括取得します。Purelymail では、Mailhearth で作成したメールボックスに対してサーバーサイド検索インデックスが標準で有効化され、快適な検索速度が保たれます。

## ログと可観測性

Mailhearth は標準の構造化テキストログを `stderr` に出力します：
```bash
docker compose logs -f --tail=100 mailhearth
```

- 標準ログレベルは `info` です；
- 詳細なネットワークハンドシェイクや IMAP IDLE の再接続ログを追跡したい場合は、`.env` に `MAILHEARTH_LOG_LEVEL=debug` を指定してください；
- **セキュリティ方針**: すべてのログは安全にマスキングされており、平文パスワード、API トークン、復号済みシークレットが出力されることはありません。

## 動作確認・テストコマンド

```powershell
# 基本テストの実行
go test ./... -run '^$'

# マルチプロバイダーモデルおよびコアロジックの検証
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'

# 静的解析
go vet ./...

# フロントエンド自動テストと本番ビルド検証
npm --prefix web run test
npm --prefix web run build
```

実際のプロバイダー環境に対する統合テストを実行する場合は、`MAILHEARTH_DEV_STACK=false` を設定し、`data/integration/multi-provider/` に認証情報を配置した上で `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG` を指定して `go test -count=1 -v ./internal/integration/multiprovider` を実行してください。
