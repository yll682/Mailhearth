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

## 設定リファレンス

すべての設定は環境変数で行います。値の形式が不正な場合や範囲外の場合は、起動時に該当する変数名を示すエラーで停止します。自動調整された値は警告として記録され、起動時には有効な設定（機密情報を除く）が出力されます。

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `MAILHEARTH_BASE_URL` | 空 | 公開 URL。招待リンクの生成に使用します。`MAILHEARTH_SECURE_COOKIES=false` を設定しない限り、`https://` である必要があります。 |
| `MAILHEARTH_LISTEN` | `:8080` | HTTP の待ち受けアドレス。 |
| `MAILHEARTH_DATA_DIR` | `./data`（コンテナ内では `/data`） | SQLite データベース、マスターキー、アップロードファイル。 |
| `MAILHEARTH_MASTER_KEY` | `<data>/master.key` に自動生成 | 保存された認証情報を暗号化する 32 バイトの鍵（base64 または hex）。 |
| `MAILHEARTH_SETUP_TOKEN` | 起動時に自動生成 | 新しいインスタンスで組織を作成する際に必要です。未設定の場合、組織が作成されるまで起動のたびにランダムなトークンが生成され、ログに出力されます（`FIRST-TIME SETUP` を探してください。フィールド名は `setupToken`）。自分で設定したトークンがログに出力されることはありません。 |
| `MAILHEARTH_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` のいずれか。 |
| `MAILHEARTH_TRUST_PROXY` | `false` | 信頼済みプロキシからの `X-Forwarded-For` / `X-Forwarded-Host` を読み取ります。 |
| `MAILHEARTH_TRUSTED_PROXIES` | ループバックとプライベートネットワーク | リバースプロキシの CIDR またはアドレス（カンマ区切り）。その他の接続元からの転送ヘッダーは無視されます。 |
| `MAILHEARTH_SECURE_COOKIES` | `true`（`MAILHEARTH_DEV_STACK` 使用時は `false`） | セッション Cookie の `Secure` 属性。`false` にするのは平文 HTTP のテスト用インスタンスに限ってください。 |
| `MAILHEARTH_ALLOWED_MAIL_NETWORKS` | 空 | メールサーバーを置いてよい CIDR。ここに記載しない限り、プライベート、ループバック、リンクローカルのアドレスへの接続は拒否されます。IMAP/SMTP/ManageSieve サーバーが内部ネットワークにある場合に設定してください。 |
| `MAILHEARTH_CA_BUNDLES_FILE` | 空 | CA バンドル ID と PEM ファイルを対応付ける JSON ファイル。例：`{"1": "/etc/mailhearth/ca.pem"}`。プロトコルエンドポイントはこの ID を参照してプライベート CA を信頼します。 |
| `MAILHEARTH_IMAP_MAX_CONNS` | `24` | IMAP 接続数の上限。プールは 4〜24 に対応し、範囲外の値は警告とともに調整されます。 |
| `MAILHEARTH_IMAP_IDLE_SECONDS` | `90` | アイドル状態の IMAP 接続を閉じるまでの秒数（10〜3600）。 |
| `MAILHEARTH_MAX_UPLOAD_MB` | `25` | 1 つの添付ファイルのアップロード上限。 |
| `MAILHEARTH_MAX_MESSAGE_MB` | `40` | エンコード後のメッセージの上限。添付ファイルは送信用にエンコードされると約 3 分の 1 大きくなります。 |
| `MAILHEARTH_UPLOAD_QUOTA_MB` | `120` | メンバー 1 人あたりの未送信アップロードの合計サイズ。常に適用され、`MAILHEARTH_MAX_UPLOAD_MB` 以上である必要があります（1〜1048576）。制限を緩めるには値を大きくしてください。 |
| `MAILHEARTH_SESSION_DAYS` | `14` | セッションのスライディング有効期限（1〜365）。 |
| `MAILHEARTH_SESSION_MAX_DAYS` | `30` | セッションの絶対有効期間（1〜3650）。`MAILHEARTH_SESSION_DAYS` より短くはできません。大きな値にすると実質的に制限を解除できます。 |
| `MAILHEARTH_INVITE_DAYS` | `7` | 招待リンクの有効日数（1〜90）。 |
| `MAILHEARTH_DEV_STACK` | `false` | 開発専用：プロセス内の疑似プロバイダー、IMAP、SMTP。 |
| `GOMEMLIMIT` | 未設定時は `160MiB` | Go ヒープの上限。 |

既定値は新規の本番環境を前提に選ばれています。最も保護的な設定が既定であり、緩める場合は必ず明示的に設定します。

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

## アップグレードとデータベーススキーマ

スキーマは `internal/db/migrations/0001_init.sql` で定義されています。空のデータディレクトリで初めて起動すると、スキーマは単一のトランザクション内で適用されます。いずれかのステートメントが失敗した場合はトランザクション全体がロールバックされ、データベースは空のまま残ります。スキーマバージョンは SQLite の `PRAGMA user_version` に記録されます。

Mailhearth は、自身が作成していないデータベース（例：プレリリース版が残したもの）や、実行中のバイナリより新しいスキーマを持つデータベースでは起動を拒否します。前者の場合は空のデータディレクトリで起動し、後者の場合は対応するリリースを実行してください。

今後のスキーマ変更は番号付きの追加ファイルとして提供され、それぞれ個別のトランザクションで適用されます。

コンテナイメージはリリースタグに対してのみ公開されます。各 `vX.Y.Z` タグから `X.Y.Z` と `X.Y` リリースライン（1.0 以降は `X` も）が生成されます。随時更新される `latest` タグはなく、ブランチへのプッシュからイメージがビルドされることもありません。`docker-compose.yml` は互換性のあるパッチリリースを受け取る `0.2` リリースラインに固定されており、新しいリリースラインへの移行はこのタグを意図的に変更して行います。

1. **アップグレード前**：バックアップを取得し（上記参照）、`master.key` のバックアップを確認してください。
2. **アップグレードの適用**：
   ```bash
   docker compose pull
   docker compose up -d
   ```
3. **ロールバック**：古いバイナリは新しいスキーマ上では動作しません。ロールバックする場合は、アップグレード前のバックアップを復元し、以前のイメージに戻してください。

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

また、監査ログとレート制限で訪問元の正確な IP アドレスを使うために、`.env` で `MAILHEARTH_TRUST_PROXY=true` を設定してください。リバースプロキシがループバックやプライベートネットワークから接続しない場合は、そのアドレスを `MAILHEARTH_TRUSTED_PROXIES` に追加してください。

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
- すべての API レスポンスには `X-Request-ID` ヘッダーが付き、エラーログの `requestId` と一致します。サーバーエラーは `error`、その他のクライアントエラーは `info` レベルで記録されます。
- **セキュリティ方針**: すべてのログは安全にマスキングされており、平文パスワード、API トークン、復号済みシークレットが出力されることはありません。

## 動作確認・テストコマンド

```powershell
# 基本テストの実行
go test ./... -run '^$'

# データベーススキーマ、ドメインモデル、HTTP API、新規インストールのテスト
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./cmd/mailhearth

# 静的解析
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...

# API レスポンス型を変更した後に web/src/lib/api-types.ts を再生成
go run ./cmd/apitypes

# フロントエンド自動テストと本番ビルド検証
npm --prefix web run test
npm --prefix web run build
```

実際のプロバイダー環境に対する統合テストを実行する場合は、`MAILHEARTH_DEV_STACK=false` を設定し、`data/integration/multi-provider/` に認証情報を配置した上で `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG` を指定して `go test -count=1 -v ./internal/integration/multiprovider` を実行してください。
