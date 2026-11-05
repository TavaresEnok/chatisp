import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { createProvider, inviteProviderAdmin, loadPlatformAccess, setProviderSuspended } from "@/api/panel";
import { emailSchema } from "@/components/auth/password";
import { PlatformUsagePanel } from "@/components/settings/platform-usage";
import { errorMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { queryKeys } from "@/lib/query-keys";
import { useSessionStore } from "@/stores/session-store";

const createSchema = z.object({
  timezone: z.string().trim().min(1, "Informe o fuso.").max(64, "Use no máximo 64 caracteres."),
});

type CreateValues = z.infer<typeof createSchema>;

const inviteSchema = z.object({
  email: emailSchema,
});

type InviteValues = z.infer<typeof inviteSchema>;

const tenantStatus: Record<string, string> = {
  active: "Ativo",
  suspended: "Suspenso",
};

export function PlatformAdmin() {
  const principal = useSessionStore((state) => state.principal);
  const enabled = principal !== null && principal.mfaEnrollmentRequired !== true;
  const access = useQuery({
    queryKey: queryKeys.platformTenants(),
    enabled,
    queryFn: loadPlatformAccess,
  });

  if (!enabled || !access.data?.allowed) {
    return null;
  }

  return (
    <section aria-labelledby="provedores-da-plataforma" className="flex flex-col gap-4">
      <h2 id="provedores-da-plataforma" className="text-base font-medium">
        Provedores da plataforma
      </h2>
      <p className="text-sm text-muted-foreground">
        Só o cadastro do provedor. Conversas e mensagens não entram aqui.
      </p>
      <CreateProviderForm />
      <PlatformUsagePanel />
      <ul aria-label="Provedores" className="flex flex-col gap-3">
        {access.data.tenants.map((tenant) => (
          <li
            key={tenant.id}
            className="flex flex-col gap-2 rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10"
          >
            <p>{tenant.timezone}</p>
            <p>{tenantStatus[tenant.status] ?? "Situação desconhecida"}</p>
            <ProviderStateButton tenantId={tenant.id} suspended={tenant.status === "suspended"} />
            {tenant.status === "active" ? <InviteProviderAdminForm tenantId={tenant.id} /> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

export function PlatformRoute() {
  const principal = useSessionStore((state) => state.principal);
  const access = useQuery({
    queryKey: queryKeys.platformTenants(),
    enabled: principal !== null && principal.mfaEnrollmentRequired !== true,
    queryFn: loadPlatformAccess,
  });

  if (!principal) {
    return <p className="text-sm">Entre para consultar provedores.</p>;
  }
  if (access.isPending) {
    return (
      <p role="status" className="text-sm">
        Carregando.
      </p>
    );
  }
  if (access.isError) {
    return (
      <p role="alert" className="text-sm text-destructive">
        {errorMessage(access.error)}
      </p>
    );
  }
  if (!access.data?.allowed) {
    return (
      <p role="alert" className="text-sm">
        O servidor não autorizou a gestão de provedores.
      </p>
    );
  }
  return <PlatformAdmin />;
}

function CreateProviderForm() {
  const queryClient = useQueryClient();
  const form = useForm<CreateValues>({
    resolver: zodResolver(createSchema),
    defaultValues: { timezone: "America/Sao_Paulo" },
  });
  const create = useMutation({
    mutationFn: (values: CreateValues) => createProvider(values.timezone),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.platformTenants() });
    },
  });

  return (
    <form
      noValidate
      className="flex max-w-md flex-col gap-3"
      onSubmit={form.handleSubmit((values) => {
        create.mutate(values);
      })}
    >
      {create.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(create.error)}
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor="fuso-do-provedor">Fuso do provedor</Label>
        <Input id="fuso-do-provedor" autoComplete="off" {...form.register("timezone")} />
        {form.formState.errors.timezone?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.timezone.message}</p>
        ) : null}
      </div>
      <Button type="submit" disabled={create.isPending} className="h-11 w-fit px-3">
        Criar provedor
      </Button>
    </form>
  );
}

function ProviderStateButton({ tenantId, suspended }: { tenantId: string; suspended: boolean }) {
  const queryClient = useQueryClient();
  const change = useMutation({
    mutationFn: () => setProviderSuspended(tenantId, !suspended),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.platformTenants() });
    },
  });

  return (
    <div className="flex flex-col gap-2">
      {change.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(change.error)}
        </p>
      ) : null}
      <Button
        type="button"
        variant="outline"
        className="h-11 w-fit px-3"
        disabled={change.isPending}
        onClick={() => {
          change.mutate();
        }}
      >
        {suspended ? "Reativar" : "Suspender"}
      </Button>
    </div>
  );
}

function InviteProviderAdminForm({ tenantId }: { tenantId: string }) {
  const fieldId = `convite-admin-${tenantId}`;
  const form = useForm<InviteValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { email: "" },
  });
  const invite = useMutation({
    mutationFn: (values: InviteValues) => inviteProviderAdmin(tenantId, values.email),
    onSuccess: () => {
      form.reset();
    },
  });

  return (
    <form
      noValidate
      className="flex max-w-md flex-col gap-2"
      onSubmit={form.handleSubmit((values) => {
        invite.mutate(values);
      })}
    >
      {invite.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(invite.error)}
        </p>
      ) : null}
      {invite.isSuccess ? (
        <p role="status" className="text-sm">
          Convite enviado por e-mail. Vale até {new Date(invite.data.expires_at).toLocaleString("pt-BR")}.
        </p>
      ) : null}
      <Label htmlFor={fieldId}>E-mail do administrador do provedor</Label>
      <Input id={fieldId} type="email" autoComplete="off" {...form.register("email")} />
      {form.formState.errors.email?.message ? (
        <p className="text-sm text-destructive">{form.formState.errors.email.message}</p>
      ) : null}
      <Button type="submit" variant="outline" disabled={invite.isPending} className="h-11 w-fit px-3">
        Convidar administrador
      </Button>
    </form>
  );
}
