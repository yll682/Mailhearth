# 実際の Purelymail アカウントに対する統合テスト

[English](integration-testing.md) · [简体中文](integration-testing.zh-CN.md) · [繁體中文](integration-testing.zh-TW.md) · **日本語** · [Español](integration-testing.es.md)

単体テストは Purelymail API のメモリ内の疑似実装に対して実行されます。その
疑似実装は Mailhearth が自己整合的であることを証明しますが、Mailhearth が
実際のサービスと一致することを証明することはできません。疑似実装はクライアント
と同じ API の読み方から書かれているためです。誤ったフィールド名、Purelymail が
想定と異なる解釈をするルール、実際には失効していないアプリパスワード、届く
ことのないメッセージを見つけられるのは実際のアカウントだけです。

`internal/integration` のテストスイートがその差を埋めます。既定ではスキップ
されるため、`make test` と CI はオフラインのまま高速に保たれます。

## 対象範囲

各テストは Mailhearth 自身のサービス層を動かし、その結果を Mailhearth の
データベースではなく実際のアカウントに対して確認します。

| テスト | 証明される内容 |
| --- | --- |
| `TestImportExistingAccount` | すでにユーザーとルールを持つアカウントに接続すると、それらを忠実に取り込み、取り込み済みとして印を付け、アプリパスワードを一切発行せず、上流の何も変更しません。2 回目の同期は何も行いません。 |
| `TestMailboxLifecycle` | メールボックスを作成すると、機能するアプリパスワードを持つ実際のユーザーが作成されます。IMAP ログイン、SMTP 送信、配信、レンダリング、送信済みコピーがすべて機能します。ローテーションはプロバイダー側で以前のパスワードを無効にします。削除はそのユーザーを削除します。 |
| `TestRoutingRules` | エイリアスはプロバイダーが遵守するルーティングルールになり、そこに宛てたメッセージが届き、そのアドレスを削除するとルールが取り消されます。 |
| `TestGroupDistribution` | グループアドレスはすべてのメンバーに届き、メンバーを削除すると上流のルールが書き換えられます。 |
| `TestSharedMailboxAccess` | 付与が誰が共有メールボックスを開けるかを決めます。管理者権限だけではメールへのアクセス権は決して与えられません。取り消すと扉が閉じます。 |
| `TestSieveRules` | Mailhearth がコンパイルした Sieve スクリプトはプロバイダーに受け入れられ、有効なスクリプトになり、受信トレイに入るのではなく実際に対象フォルダーへメッセージを振り分けます。 |
| `TestOffboarding` | 退職する人は、デスクトップのメールクライアントが保持していた認証情報を含め、あらゆる入り口を失います。一方、そのメールボックスと履歴は後任者に引き継がれます。 |
| `TestSuspendAndReactivate` | 停止は全員を締め出しますが、メッセージの受信は続けます。再有効化するとアクセスが戻り、その間に届いたメッセージもそこにあります。 |
| `TestPasswordResetForExternalClients` | Thunderbird に渡されたパスワードは実際に認証を通り、Mailhearth 自身のアプリパスワードはリセットを生き延び、それとは別のままです。 |
| `TestExternalForwarding` | アカウント外のアドレスへの転送は、想定どおりのルールになります。 |
| `TestTokenRejection` | 誤った API トークンは拒否され、保存されている正常な接続を壊しません。 |

## 安全性

このテストスイートは実際のメールボックスと実際のルーティングルールを作成
および削除し、Purelymail のユーザーを削除するとそのメールも削除されます。
2 つの仕組みがそれを限定された範囲にとどめます。

テストスイートが作成するすべてのオブジェクトは
`<prefix>-<runid>-<role><n>` という名前になり、prefix の既定値は `mh-it`
です。破壊的なヘルパーはアドレスに触れる前に `guardOwned` を呼び出し、その
アドレスが設定されたテストドメイン上にあり、**かつ** prefix を持たない限り
実行を中止します。テストは、たとえコードの変更がそう命じても、自分が作成
していないメールボックスを削除することはできません。

各テストは作成したオブジェクトをその都度後処理のために登録するため、中断
された実行や失敗した実行でも、作成したものは破棄されます。

それでも、**何も大事なものが入っていないドメインにこのテストスイートを
向けてください。** アカウント上の専用のテストドメインが適切な構成です。
この保護はテストスイートの誤動作に対するものであり、`MAILHEARTH_IT_DOMAIN`
の打ち間違いがたまたま本番のドメインと prefix を指してしまう場合には及び
ません。

各テストは一時ディレクトリに独自のマスターキーを持つ独自の空のデータベース
を構築するため、実際のインストールには何も触れません。

## 実行方法

配信こそがこれらのテストの大半が測るものなので、ドメインは機能する MX
レコードとともに Purelymail アカウントにすでに存在している必要があります。
API トークンには完全なアクセス権が必要です。

```bash
export MAILHEARTH_IT_TOKEN=your-api-token
export MAILHEARTH_IT_DOMAIN=test.example.com

go test ./internal/integration -v -timeout 40m
```

テストスイート全体には数分かかると見込んでください。実時間の大半はメッセージ
が実際に届くのを待つことに費やされます。反復中に 1 つのテストだけを実行する
場合:

```bash
go test ./internal/integration -run TestMailboxLifecycle -v -timeout 15m
```

テストはアカウント上でユーザーを作成および削除します。Purelymail はユーザー
単位で課金するため、1 回の完全な実行にかかる費用は 1 セントの何分の一かです。

## 設定

| 環境変数 | 既定値 | 目的 |
| --- | --- | --- |
| `MAILHEARTH_IT_TOKEN` | — | API トークン。必須です。これがないとテストスイートはスキップされます。 |
| `MAILHEARTH_IT_DOMAIN` | — | テストドメイン。必須です。これがないとテストスイートはスキップされます。 |
| `MAILHEARTH_IT_PREFIX` | `mh-it` | テストスイートが所有するオブジェクトを示すローカル部分のプレフィックス。`mh` で始まる必要があります。 |
| `MAILHEARTH_IT_EXTERNAL` | — | アカウント外のアドレス。`TestExternalForwarding` を有効にします。 |
| `MAILHEARTH_IT_DELIVER_SECONDS` | `180` | 失敗するまでにメッセージの到着を待つ時間。 |
| `MAILHEARTH_IT_KEEP` | 未設定 | 作成したオブジェクトを調査用に残します。自分で後処理する必要があります。 |
| `MAILHEARTH_IT_API_URL` | `https://purelymail.com/api/v0` | API エンドポイント。 |
| `MAILHEARTH_IT_IMAP_ADDR` | `imap.purelymail.com:993` | IMAP アドレス。 |
| `MAILHEARTH_IT_SMTP_ADDR` | `smtp.purelymail.com:465` | サブミッションアドレス。 |
| `MAILHEARTH_IT_SIEVE_ADDR` | `mailserver.purelymail.com:4190` | ManageSieve アドレス。空にすると Sieve テストをスキップします。 |
| `MAILHEARTH_IT_IMAP_TLS` | `tls` | `tls`、`starttls`、`none` のいずれか。 |
| `MAILHEARTH_IT_SMTP_TLS` | `tls` | `tls`、`starttls`、`none` のいずれか。 |
| `MAILHEARTH_IT_SIEVE_TLS` | `starttls` | `tls`、`starttls`、`none` のいずれか。 |

## テストが失敗したとき

配信のタイムアウトがよくある失敗で、通常は Mailhearth が壊れているのでは
なく、ドメインの DNS が不完全であることを意味します。まず MX と SPF の
レコードを確認し、その後 `MAILHEARTH_IT_DELIVER_SECONDS` を引き上げて
ください。Purelymail のスパムフィルタリングがテストメッセージを迷惑メール
フォルダーに振り分けることもあります。失敗メッセージには、検索した
フォルダーとそこで見つかったメッセージ数が示されます。

`MAILHEARTH_IT_KEEP=1` を付けて再実行すると、オブジェクトをそのまま残して
Purelymail の Web インターフェースで確認できます。残したオブジェクトは
費用がかかり続け、次回の実行が引き継ぐこともないため、後で削除することを
忘れないでください。

後処理が行われないまま実行が強制終了された場合、残ったものは簡単に見つかり
ます。そのすべてが prefix を持っています。

## CI から外しておく

このテストスイートをプルリクエストのパイプラインに組み込まないでください。
実際のトークンが必要で、費用がかかり、配信の待ち時間のせいでマージ前の
ゲートとしては遅すぎます。リリースの前に、また Purelymail クライアント、
IMAP や SMTP の経路、Sieve コンパイラに変更を加えた後に実行してください。
