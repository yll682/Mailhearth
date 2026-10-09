import { useRef } from "preact/hooks";
import { OperationRequests } from "./operationRequest";

export function useOperationRequests(): OperationRequests {
  const requests = useRef<OperationRequests | null>(null);
  if (!requests.current) requests.current = new OperationRequests();
  return requests.current;
}
