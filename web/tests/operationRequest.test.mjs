import assert from "node:assert/strict";
import test from "node:test";
import { OperationRequests } from "../src/lib/operationRequest.ts";

test("相同操作和请求内容使用同一个 requestId", () => {
  const requests = new OperationRequests();
  const first = requests.prepare("mailbox/23", { expectedRevision: 2, targets: ["member@example.org"] });
  const repeated = requests.prepare("mailbox/23", { expectedRevision: 2, targets: ["member@example.org"] });
  assert.equal(repeated.requestId, first.requestId);
  assert.match(first.requestId, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test("不同操作与更新的请求内容具有独立 requestId", () => {
  const requests = new OperationRequests();
  const original = { expectedRevision: 1, credentials: [{ secret: "entered-test-password" }] };
  const first = requests.prepare("endpoint/23", original);
  const other = requests.prepare("endpoint/24", original);
  const updated = requests.prepare("endpoint/23", { ...original, expectedRevision: 2 });
  assert.notEqual(first.requestId, other.requestId);
  assert.notEqual(first.requestId, updated.requestId);
  assert.equal(Object.hasOwn(original, "requestId"), false);
});

test("完成操作后再次执行产生新的 requestId", () => {
  const requests = new OperationRequests();
  const first = requests.prepare("discover/1", {});
  requests.complete("discover/1");
  assert.notEqual(requests.prepare("discover/1", {}).requestId, first.requestId);
});
