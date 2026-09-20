import { useEffect, useState } from "preact/hooks";
import { t } from "@/lib/i18n";
import { get, post, type AccessibleMailbox, type TeamState, type Member } from "@/lib/api";
import { me, errorToast } from "@/lib/state";
import { fmtDate } from "@/lib/format";
import { Button, Icon, Avatar } from "@/ui";

export function TeamPanel({ mailbox, msgKey, state, onChange }: { mailbox: AccessibleMailbox; msgKey: string; state: TeamState; onChange: (s: TeamState) => void }) {
  const [members, setMembers] = useState<Member[]>([]);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    get<Member[]>(`/api/mail/mailboxes/${mailbox.id}/team`).then(setMembers, () => {});
  }, [mailbox.id]);
  const act = async (body: Record<string, unknown>) => {
    setBusy(true);
    try {
      onChange(await post<TeamState>(`/api/mail/mailboxes/${mailbox.id}/team`, { key: msgKey, ...body }));
    } catch (e) {
      errorToast(e);
    } finally {
      setBusy(false);
    }
  };
  const myId = me.value!.member.id;
  const label = (a: string) => t(a);
  return (
    <div class="team-panel">
      <div class="team-head">
        <Icon name="users" size={16} />
        <b>{t("Team")}</b>
        <div class="spacer" />
        <label class="field-inline">
          <span class="muted">{t("Assigned to")}</span>
          <select value={state.assigneeId ?? ""} onChange={(e) => act({ action: "assign", memberId: Number((e.target as HTMLSelectElement).value) || null })} disabled={busy}>
            <option value="">{t("Unassigned")}</option>
            {members.map((m) => (
              <option key={m.id} value={m.id}>
                {m.displayName}
              </option>
            ))}
          </select>
        </label>
        {state.assigneeId !== myId ? (
          <Button size="sm" onClick={() => act({ action: "assign", memberId: myId })} disabled={busy}>
            {t("Assign to me")}
          </Button>
        ) : null}
        {state.status === "resolved" ? (
          <Button size="sm" icon="refresh" onClick={() => act({ action: "status", status: "open" })} disabled={busy}>
            {t("Reopen")}
          </Button>
        ) : (
          <Button size="sm" kind="primary" icon="check" onClick={() => act({ action: "status", status: "resolved" })} disabled={busy}>
            {t("Resolve")}
          </Button>
        )}
      </div>
      <ul class="timeline">
        {state.activity.map((a) => (
          <li key={a.id}>
            <Avatar name={a.memberName || t("System")} size={22} />
            <div>
              <span>
                <b>{a.memberName || t("System")}</b> {label(a.action)} {a.action === "note" ? "" : a.detail ? <span class="muted">{a.detail}</span> : null}
              </span>
              {a.action === "note" ? <div class="note-body">{a.detail}</div> : null}
              <span class="muted small">{fmtDate(a.createdAt, "full")}</span>
            </div>
          </li>
        ))}
      </ul>
      <form
        class="note-form"
        onSubmit={async (e) => {
          e.preventDefault();
          if (!note.trim()) return;
          await act({ action: "note", text: note });
          setNote("");
        }}
      >
        <input placeholder={t("Internal note")} value={note} onInput={(e) => setNote((e.target as HTMLInputElement).value)} />
        <Button size="sm" busy={busy} type="submit">
          {t("Add note")}
        </Button>
      </form>
      <p class="muted small">{t("Notes are only visible to your team.")}</p>
    </div>
  );
}
