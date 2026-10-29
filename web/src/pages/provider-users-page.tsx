import { RequireRole } from "@/components/auth/require-role";
import { ProviderAdmin } from "@/components/settings/provider-admin";

export function ProviderUsersPage() {
  return (
    <article className="flex max-w-3xl flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Usuários do provedor</h1>
      <RequireRole role="admin">
        <ProviderAdmin />
      </RequireRole>
    </article>
  );
}
