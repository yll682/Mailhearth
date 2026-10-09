import { t } from "./i18n";

export function errorLabel(code: string, message = ""): string {
  const labels: Record<string, string> = {
    address_exists: "This address is already registered.", identity_exists: "This identity is already registered.",
    api_replaced: "Use the current connection-specific interface.",
    connection_in_use: "This resource has dependencies.", domain_in_use: "This resource has dependencies.", mailbox_in_use: "This resource has dependencies.", member_in_use: "This resource has dependencies.",
    credential_decryption_failed: "The saved credential could not be decrypted.",
    endpoint_disabled: "This protocol or connection is disabled.", endpoint_unconfigured: "Configure this protocol before using it.",
    external_action_required: "Complete the action in the provider console and report the result.",
    folder_mapping_required: "Configure the required special folder.", forbidden: "You do not have permission for this action.",
    idempotency_conflict: "This request ID was already used for different content.",
    invalid: "Check the request fields and current resource state.", invalid_operation_result: "The operation result is incomplete.", internal: "An internal error occurred.",
    mailbox_has_history: "Archive this mailbox to retain its history.", not_found: "The requested resource was not found.",
    operation_in_progress: "Another operation is using this resource.", operation_not_cancellable: "This operation cannot be cancelled in its current state.", operation_not_reconcilable: "This operation cannot be checked in its current state.", operation_not_retryable: "This operation cannot be retried in its current state.",
    provider_auth_failed: "Management authentication failed.", mail_auth_failed: "Protocol authentication failed.", unsupported_auth_mechanism: "The server does not support the required authentication mechanism.",
    remote_result_unknown: "The remote result requires checking.", verification_required: "This action requires verification.", revision_conflict: "The configuration changed. Refresh before continuing.",
    secret_already_claimed: "This secret has already been claimed.", secret_expired: "The secret claim has expired.",
    sieve_extension_missing: "The server lacks a required Sieve extension.", sieve_takeover_required: "Confirm takeover of the active Sieve script.",
    staging_missing: "The staged message is unavailable.", draft_locator_changed: "The draft location changed.", submission_content_changed: "The staged message content changed.",
    submission_not_retryable: "This sending request cannot be retried in its current state.", submission_unknown: "Delivery result unknown. Sending again may deliver a duplicate.",
    target_constraint_failed: "The targets do not meet this provider's restrictions.", unsupported_auth_mode: "The authentication mode is unsupported.", unsupported_operation: "This operation is unsupported.", upstream_failed: "The remote service request failed.",
  };
  return labels[code] ? `${t(labels[code])} (${code})` : message ? t(message) : code;
}
