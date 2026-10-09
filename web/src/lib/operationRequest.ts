export class OperationRequests {
  private requests = new Map<string, { body: string; requestId: string }>();

  prepare<T extends object>(action: string, body: T): T & { requestId: string } {
    const serialized = JSON.stringify(body);
    if (serialized === undefined) throw new TypeError("Operation request must be serializable");
    let request = this.requests.get(action);
    if (!request || request.body !== serialized) {
      request = { body: serialized, requestId: crypto.randomUUID() };
      this.requests.set(action, request);
    }
    return { ...body, requestId: request.requestId };
  }

  complete(action: string): void {
    this.requests.delete(action);
  }
}
