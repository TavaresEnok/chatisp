import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";
import { resetSessionStore } from "@/stores/session-store";

afterEach(() => {
  cleanup();
  resetSessionStore();
  localStorage.clear();
  sessionStorage.clear();
});
