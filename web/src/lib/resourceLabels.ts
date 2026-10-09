import { t } from "./i18n";
import type { DiscoveryResource } from "./api";

export function statusLabel(value: string): string {
  const labels: Record<string, string> = { never: "Not checked", passed: "Passed", failed: "Failed", stale: "Check expired", unverified: "Unverified", unknown: "Unknown", synced: "Synchronized", pending: "Pending", error: "Error", active: "Active", pending_confirmation: "Awaiting confirmation", blocked: "Blocked", present: "Present", missing: "Missing", inaccessible: "Inaccessible", external: "Externally managed", redirect: "Redirect", copy: "Keep a copy and forward", queued: "Queued", running: "Running", succeeded: "Succeeded", needs_action: "Action required", cancelled: "Cancelled", disabled: "Disabled", ready: "Ready", unconfigured: "Not configured", automatic: "Automatic", unsupported: "Unsupported", degraded: "Degraded" };
  Object.assign(labels, { api: "API management", accepted: "Accepted", not_started: "Not started", sending: "Sending", saving: "Saving", saved: "Saved", skipped: "Skipped" });
  return t(labels[value] ?? value);
}

export function resourceLabel(item: DiscoveryResource): string {
  if (item.resource.purpose === "forwarding") return t("Mailbox forwarding");
  const labels: Record<string, string> = { domain: "Domain", mailbox: "Mailbox", routing_rule: "Address rule", alias: "Alias", external_rule: "External rule", identity: "Identity" };
  return t(labels[item.resource.resourceType] ?? item.resource.resourceType);
}

export function canImportResource(item: DiscoveryResource): boolean {
  return ["domain", "mailbox", "routing_rule", "alias", "external_rule", "identity", "forwarding"].includes(item.resource.resourceType);
}

export function resourceDescription(item: DiscoveryResource): string {
  if (item.forwarding) return `${item.forwarding.localPart}@${item.forwarding.domain} → ${item.forwarding.targets.join(", ")}`;
  const description = item.summary.address ?? item.summary.name ?? item.resource.remoteKey;
  return item.summary.mailboxAddress ? `${item.summary.mailboxAddress} → ${description}` : description;
}
