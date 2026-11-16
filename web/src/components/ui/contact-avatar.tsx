// Deterministic initials avatar. The spec (Avatar, V1 simples) shows a photo only if WhatsApp
// supplies one; the Cloud API does not, and no authorized image source exists, so every
// contact gets initials on a neutral surface. contactHue stays available as a stable,
// deterministic value per name, but the avatar no longer paints with it. It never loads a remote URL.
// It is decorative: the contact name next to it is the accessible name.

export function contactInitials(label: string): string {
  const words = label
    .normalize("NFC")
    .split(/[\s._-]+/u)
    .map((w) => Array.from(w.replace(/[^\p{L}\p{N}]/gu, ""))[0] ?? "")
    .filter(Boolean);
  if (words.length === 0) return "?";
  const first = words[0] ?? "";
  const last = words[words.length - 1] ?? "";
  return (words.length === 1 ? first : first + last).toLocaleUpperCase("pt-BR");
}

export function contactHue(label: string): number {
  let h = 0;
  for (const ch of label.normalize("NFC").toLocaleLowerCase("pt-BR")) h = (h * 31 + ch.codePointAt(0)!) % 360;
  return h;
}

export function ContactAvatar({ label, size = 32 }: { label: string; size?: number }) {
  return (
    <span
      aria-hidden="true"
      data-testid="contact-avatar"
      className="inline-flex shrink-0 select-none items-center justify-center rounded-full text-xs font-semibold"
      style={{
        width: size,
        height: size,
        // Neutral surface from the theme tokens: readable in the light and dark themes.
        backgroundColor: "var(--secondary)",
        color: "var(--soft, var(--foreground))",
      }}
    >
      {contactInitials(label)}
    </span>
  );
}
