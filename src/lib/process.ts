import type { ClashStatus } from "../mygo";

export type ProcessState = "disconnected" | "waiting" | "error" | "enabled" | "partial" | "disabled" | "unknown";

/** Actual core state, independent of the maintenance checkbox or its draft. */
export function processState(status: ClashStatus | null): ProcessState {
  if (!status?.connected) return "disconnected";
  if (status.processCheckError) return "error";
  if (!status.processCheckedAt) return "waiting";
  if (status.findProcessMode === "always") return "enabled";
  if (status.findProcessMode === "strict") return "partial";
  if (status.findProcessMode === "off") return "disabled";
  return "unknown";
}
