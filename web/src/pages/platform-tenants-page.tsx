import { PlatformRoute } from "@/components/settings/platform-admin";

export function PlatformTenantsPage() {
  return (
    <article className="flex max-w-3xl flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Provedores</h1>
      <PlatformRoute />
    </article>
  );
}
