# アーキテクチャ

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · **日本語** · [Español](architecture.es.md)

## 配置とコンポーネント

Mailhearth は Preact の静的ファイルを組み込んだ単一の Go バイナリと SQLite
（modernc、cgo 不使用）で動作します。メールサーバーが本文、配信、フィルタリングを
担当します。SQLite は組織データ、暗号化された認証情報、リソース関連付け、操作、
送信リクエストと共同作業メタデータを保存します。Node はフロントエンドのビルドに使用します。

- `cmd/mailhearth`：設定、マスターキー、データベース、IMAP プール、ワーカー、HTTP server。
- `internal/config`、`internal/secrets`：環境設定、AES-256-GCM/HKDF、argon2id、token。
- `internal/db`、`internal/model`：組み込み migration、関連付け検証、永続化モデル。
- `internal/provider`：共通管理インターフェースと Purelymail、Migadu、manual アダプター。
- `internal/purelymail`：対応するアダプター用の Purelymail API クライアント。
- `internal/core`：接続、検出と取り込み、組織、リソースのライフサイクル、Operation、Submission。
- `internal/mailproto/imappool`：接続上限、endpoint バージョンの無効化、IDLE 監視。
- `internal/mailproto/mailops`、`mimeutil`、`sieve`：IMAP/SMTP、MIME サニタイズ、Sieve、ManageSieve。
- `internal/httpapi`、`internal/web`、`web/`：権限検証、JSON/SSE、組み込みファイル、メール・管理・設定 UI。

## 組織とリソースの所属

各配置に一つの Organization があり、Member はロール、部署と `invited`、`active`、
`disabled`、`departed` の状態を持ちます。Role は名前付き権限を含みます。personal
Mailbox の所有権と shared Mailbox の明示的な権限（`full`、`send`、`read`）がメール
アクセスを制御します。管理権限はメールアクセスを付与しません。

MailConnection は組織に属し、プロバイダー種類、名称、管理 API 認証、ドメイン範囲、
三つのプロトコルの既定設定を持ちます。Domain は論理名で、DomainBinding は接続、
プロバイダー設定と DNS 状態を関連付けます。Mailbox のアドレスは接続内で一意です。
別の接続の同一アドレスは独立して保持されます。

Address は `primary`、`alias`、`forward`、`group`、`catchall`、`prefix` と外部管理の
`external_rule` を表します。転送は独立した `mailbox_forwardings` に保存し、取り込み時も
primary アドレスを保持します。Identity の表示設定と SMTP の送信者認可は独立しています。
受信エイリアスは From の使用を許可しません。Group は適格な personal メールボックスを
すべて計算し、接続・ドメイン制限を検証してから宛先の重複を除きます。

ProviderResource は各リモート参照を一つのローカルオブジェクトと用途に関連付け、
所有、リモート状態と安全な構造化観測結果を記録します。参照制約と revision 検証が
組織・接続の境界と履歴を保護します。

## プロトコル設定

Mailbox は独立した IMAP、SMTP、ManageSieve endpoint を持ちます。network mode は
`inherit`、`override`、`disabled` です。有効な endpoint は username と暗号化された
Credential を個別指定します。認証情報は `managed` または明示入力です。候補設定は
すべての有効なプロトコルで認証を通過してから単一トランザクションで確定します。
endpoint、接続、認証情報、アクセスのバージョン変更は古い接続を閉じます。

Purelymail の既定値は IMAP `imap.purelymail.com:993` TLS、SMTP
`smtp.purelymail.com:465` TLS、ManageSieve `mailserver.purelymail.com:4190` STARTTLS。
Migadu は IMAP `imap.migadu.com:993` TLS、SMTP `smtp.migadu.com:465` TLS、ManageSieve
無効です。手動接続は明示設定まで全項目無効です。TLS はシステム証明書または明示的な
プライベート CA 証明書セットで hostname を検証します。

## 検出、取り込みと管理操作

検出は選択範囲を完全に読み、接続 revision と期限を持つスナップショットを保存します。
取り込みは所属、バージョン、期限、依存関係を一つのローカルトランザクションで検証します。
取り込まれた personal Mailbox は owner とログイン認証情報を持ちません。同期は登録済み
リソースの観測を更新し、新規リソースは選択待ちにします。owner、アクセス権、共同作業、
署名と entered 認証情報は保持します。

転送の取り込みは転送元、選択した宛先、リモート参照と `active`、`pending_confirmation`、
`blocked`、`unknown` の確認状態を保持します。観測された宛先・配信方式と希望設定を
個別保存します。未検証の方式は `unverified` のままです。API 上の存在と管理者報告は
実配信を証明しません。Migadu の転送書き込みには引き続き V03 検証が必要です。

Operation は requestId、内容ハッシュ、暗号化ペイロード、リソース占有と各ステップを
保存します。重複送信は同じ操作を返し、revision 変更や権限失効は古い設定の実行を防ぎます。
不明なリモート書き込みは照合が必要で、確認済みステップを繰り返しません。外部処理は
管理者報告と `systemVerified=false` を記録します。認証情報作成の応答が失われリモート ID
がない場合は明示的な削除報告が必要です。停止は直ちにローカルアクセスを失効し、アーカイブは
履歴を保持します。退職処理は移管、グループ、認証情報失効を個別に記録します。

## メールと送信経路

1. HTTP が現在の所有権・権限と対象プロトコル endpoint を検証します。
2. IMAP の既定上限は全体 24、接続ごと 8、Mailbox ごと 3 です。IDLE 監視中も通常リクエスト
   用に全体 4、接続ごと 2 の接続枠を確保します。
3. `mailops` は IMAP でフォルダー、ページング、検索と MIME を読みます。明示マッピング、
   一意の SPECIAL-USE、一意の名前で特殊フォルダーを選びます。
4. `mimeutil` が HTML/CSS をサニタイズし、`cid:` を解決してリモート画像をブロックします。
   厳格な CSP の独立文書をスクリプトなしの sandbox iframe に表示します。
5. Submission は requestId、固定 Message-ID、ハッシュ、暗号化 envelope を保存し、本文は
   サーバー Drafts に保持します。SMTP accepted と Sent コピーを個別記録し、不明な配信は
   自動再試行しません。コピー再試行は SMTP を保持し、下書き削除には UID EXPUNGE が必要です。
6. 共有 IDLE 監視が SSE で通知します。共同作業は `mid:<message-id>` を使用し、
   `message_state` が担当・状態、`mail_activity` が返信・転送・割り当て・メモを保存して、
   フォルダー移動後も保持します。

## ルールとフロントエンド

構造化ルールはサーバーが宣言する拡張機能に合わせて Sieve にコンパイルします。
ManageSieve は Mailbox endpoint と go-managesieve を使用します。有効化前に現行スクリプトの
hash を確認し、引き継ぎの承認、独立候補の読み戻し、有効化の検証後にローカル設定を更新します。
既存スクリプトは保持します。ルールと自動返信は有効な endpoint と必要な拡張機能に依存します。

Preact、`@preact/signals` と history router が `/mail`、`/admin`、`/settings`、`/login`、
`/invite/:token`、`/setup` を提供します。860 px より広い画面は三つのペイン、それ以下は
ドロワーと一つのペインです。English のキーに zh-CN、zh-TW、日本語、Español の辞書を
用意し、TypeScript AST で網羅性と補間を検証します。ビルドは既存の hash ファイルを保持します。

ローカルのデータベース、HTTP とビルド検証は[結合テスト](integration-testing.ja.md)を
参照してください。実際のプロバイダーでの受け入れ検証は未完了です。
