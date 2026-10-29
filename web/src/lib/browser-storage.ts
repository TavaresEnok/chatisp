export function clearBrowserStorage() {
  try {
    localStorage.clear();
  } catch {
    // Ambiente sem armazenamento local.
  }
  try {
    sessionStorage.clear();
  } catch {
    // Ambiente sem armazenamento de sessão.
  }
}
