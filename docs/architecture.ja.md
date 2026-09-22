# アーキテクチャ

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · [繁體中文](architecture.zh-TW.md) · **日本語** · [Español](architecture.es.md)

## 目標と非目標

Mailhearth は、Purelymail の信頼性が高く安価なメール基盤を、小規模な組織が導入し、
管理し、日々使える製品として包み込む。SMTP サーバーの運用、メールの一次コピーの
保存、スパムのフィルター処理、DLP / eDiscovery / MDM の実装を意図的に**行わない**。
Purelymail ポータルや別の Roundcube の見た目を変えたものでもない。管理の単位は
組織に属する一人の人物であり、アカウント上の「ユーザー」ではない。

設計を形づくった制約:

- **極小のホスト。** 対象となるデプロイは、入手できる最も安価な VPS 上で動作する。
  サーバーは単一の静的 Go バイナリで、SQLite、プロセス内の上限付き IMAP
  コネクションプール、51 KB（gzip 圧縮後）の Preact フロントエンドで構成される。
  実行時に Redis も Postgres も Node も不要で、少数のゴルーチン以外にバックグラウンド
  ワーカーはない。
- **メールの正は Purelymail にある。** メッセージが Mailhearth のデータベースに
  コピーされることはない。クライアントが表示するものはすべて IMAP 経由で
  オンデマンドに取得し、データベースが保持するのは組織モデルと、`Message-ID` を
  キーとする少量の共同作業メタデータだけである。
- **ブラウザーに秘密は置かない。** API トークンとメールボックスのアプリパスワードは
  暗号化された状態で SQLite に保存される。ブラウザーが通信するのは Mailhearth
  だけである。

## コンポーネント

| パッケージ | 責務 |
|---|---|
| `cmd/mailhearth` | エントリーポイント: 設定、マスターキー、データベース、IMAP プール、HTTP サーバー |
| `internal/config` | 環境設定 |
| `internal/db` | SQLite（modernc、cgo 不使用）と埋め込みマイグレーション |
| `internal/secrets` | AES-256-GCM ボックス（マスターキーからの HKDF）、argon2id、トークン |
| `internal/purelymail` | 型付き API クライアント。`fake/` はインメモリの Purelymail |
| `internal/model` | サービスと API が共有する組織の型 |
| `internal/core` | サービス: セットアップ/インポート、メンバー、ロール、ドメイン、メールボックス、アドレス、グループ、オフボーディング、チーム状態 |
| `internal/mailproto/imappool` | 上限付き IMAP コネクションプールと IDLE ウォッチャー |
| `internal/mailproto/mailops` | フォルダー、一覧、レンダリング、操作、作成、SMTP |
| `internal/mailproto/mimeutil` | HTML サニタイザー、テキスト/HTML 変換、デコード |
| `internal/mailproto/sieve` | ルールモデルから Sieve へのコンパイラー。ManageSieve クライアント |
| `internal/httpapi` | JSON API、セッション、CSRF、アップロード、SSE、サンドボックス化されたメッセージ表示 |
| `internal/web` | gzip と immutable キャッシュを備えた埋め込み SPA |
| `internal/devstack` | 開発とテスト用の偽の Purelymail、IMAP、SMTP |
| `web/` | Preact + Vite のシングルページアプリ（メール、管理、設定） |

## 組織モデル

| 概念 | 意味 | 保存先 |
|---|---|---|
| **Organization** | インストールの唯一のテナント。 | `organizations` |
| **Member** | Mailhearth にサインインする実在の人物。ロール、状態（invited/active/disabled/departed）、役職、部署を持つ。 | `members` |
| **Role** | 名前を付けた権限の集合（`members.manage`、`shared.manage`、…）。組み込み: owner、admin、member。カスタムロールも可能。 | `roles` |
| **Domain** | Purelymail アカウント上のドメイン。DNS の健全性を伴う。 | `domains` ↔ Purelymail ドメイン |
| **Mailbox** | メールを保存するログインアカウント。`personal`（1 人のメンバーが所有）または `shared`（組織が所有し、複数のメンバーが共同で扱う）。 | `mailboxes` ↔ Purelymail ユーザー |
| **Address** | メールを受け取るもの: メールボックス自身のアドレス（`primary`）、1 つのメールボックスへの `alias`、任意の宛先への `forward`、`group` の配布アドレス、`catchall` または `prefix` ルール。 | `addresses` ↔ Purelymail ルーティングルール |
| **Identity** | メールボックスが送信元として使える From アドレス + 表示名 + 署名。 | `identities` |
| **Group** | メンバーの集合。任意で、メンバーシップに従って宛先が決まる配布アドレスを持つ。 | `groups`、`group_members` |
| **Access grant** | メンバー → メールボックス。レベルは `full`/`send`/`read`。 | `mailbox_access` |

1 人のメンバーが複数のメールボックスを所有できる。1 つのメールボックスは複数の
アドレスを持てる。1 つのアドレスは（転送やグループを通じて）複数のメンバーに届く。
人のロールが変わったり、離職したりしても、メールボックスとアドレスは組織に残る。
所有権は再割り当てされ、暗黙に削除されることはない。

## Purelymail へのマッピング

| Mailhearth の操作 | Purelymail API 呼び出し |
|---|---|
| 接続 | `checkAccountCredit`（トークンを検証） |
| インポート / 同期 | `listDomains`、`listUser`、`listRoutingRules` — 読み取り専用、冪等 |
| メールボックスの作成 | `createUser`（ランダムなパスワード、ウェルカムメールなし）+ `createAppPassword` |
| インポート済みメールボックスの接続 | `createAppPassword`（既存のパスワードは不要） |
| 認証情報のローテーション | `createAppPassword` の後に古いものを `deleteAppPassword` |
| 外部クライアント向けのパスワードリセット | `modifyUser{newPassword}` + ローテーション |
| 停止 / オフボーディング時のロックアウト | `modifyUser{newPassword}` + `deleteAppPassword` |
| エイリアス / 転送 / キャッチオール / 接頭辞 / グループアドレス | `createRoutingRule` / `deleteRoutingRule` |
| メールボックスでの転送 | メールボックス自身のアドレスに対するルーティングルール（Purelymail の意味論: ルールが配送より優先される） |
| ドメインの追加 / DNS の再確認 / 設定 | `addDomain`、`updateDomainSettings`、`getOwnershipCode` |

Mailhearth はメールボックスごとに「Mailhearth」という名前のアプリパスワードを
ちょうど 1 つ保持する。メンバーがそれを見ることはない。サーバーは、そのメンバーが
メールボックスを所有しているか許可を持っているかを確認したうえで、メンバーに代わって
IMAP、SMTP、ManageSieve にそれを使う。管理権限はメールへのアクセスを許可しない。
共有メールボックスの読み取りには常に明示的な許可が必要である。

## メール経路

1. `httpapi` がサインイン済みのメンバーのメールボックスを解決し
   （`core.ResolveMailbox`）、認証情報を取得する。
2. `imappool.Get` がプールされた接続を返す（合計の上限は
   `MAILHEARTH_IMAP_MAX_CONNS`、認証情報ごとにアイドル 2 本、90 秒のアイドル後に
   回収される）。
3. `mailops` が IMAP コマンドを実行する。フォルダーには `LIST-STATUS` 付きの
   `LIST`、ページングには envelope + flags + `BODYSTRUCTURE` のシーケンス範囲
   `FETCH`、クエリには `UID SEARCH`、レンダリングには `BODY.PEEK[part]`、利用できる
   場合は `MOVE`/`UIDPLUS` をフォールバック付きで使う。
4. HTML 本文は `mimeutil.SanitizeHTML`（bluemonday の許可リスト、CSS のクリーニング、
   `cid:` の解決、リモート画像のブロック）を通り、`default-src 'none'` CSP を付けた
   別のドキュメントとして配信され、サンドボックス化された iframe に表示される。
5. 送信は go-message で RFC 5322 を組み立て、同じ認証情報で SMTP 経由で送信し、
   その後 Sent に追加して元のメッセージを返信済み/転送済みにする。
6. IDLE ウォッチャー（メールボックス + フォルダーごとに 1 つ、開いているすべての
   タブで共有される）が変更を Server-Sent Events でブラウザーにプッシュする。

## 共有メールボックスの共同作業

共同作業の状態は `mid:<message-id>` をキーとするため、フォルダー間の移動後も残る。
`message_state` は担当者と対応中/解決済みの状態を保持する。`mail_activity` は
追記専用のログ（返信、転送、担当者の割り当て、メモ …）で、明示的なチーム操作からも、
メンバーが共有メールボックスから送信したときにも書き込まれる。メッセージ一覧は行に
「返信者」、担当者、状態のバッジを付ける。

## ルール

メンバーはルールを構造化された条件とアクションとして編集する。`sieve.Compile` は
それら（および休暇の自動返信）を、Purelymail が告知している拡張機能
（`fileinto imap4flags copy body vacation`）を備えた Sieve スクリプトに変換する。
スクリプトは ManageSieve（`mailserver.purelymail.com:4190`、STARTTLS）経由で
`mailhearth` としてアップロードされ、有効化される。構造化された形式は
`mailboxes.settings_json` に保存される。

## フロントエンド

Preact + `@preact/signals`、60 行の history router を使い、UI フレームワークは
使わない。ルート: `/mail/:mailbox/:folder/:uid`、`/admin/:section/:id`、
`/settings/:tab`、`/login`、`/invite/:token`、`/setup`。文字列は英語のキーで、
zh-CN の辞書を伴う。レイアウトは 860 px 以上では 3 ペインのメールクライアント、
それ未満ではドロワー + 単一ペインになる。
