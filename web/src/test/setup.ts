import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
import { useSessionStore } from "@/stores/session-store";

afterEach(() => {
  cleanup();
  useSessionStore.setState({ tenantId: null, displayName: "" });
});
