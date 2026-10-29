import { zodResolver } from "@hookform/resolvers/zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { z } from "zod";
import {
  inviteMember,
  listMemberships,
  listSessions,
  revokeSession,
  setMembershipActive,
} from "@/api/panel";
import { errorMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useEndSession } from "@/components/auth/use-end-session";
import { emailSchema } from "@/components/auth/password";
import { formatWhen } from "@/lib/format";
import { queryKeys } from "@/lib/query-keys";
import { isProviderAdmin, providerRoles, roleLabel } from "@/lib/roles";
import { useSessionStore } from "@/stores/session-store";

const inviteSchema = z.object({
  email: emailSchema,
  role: z.enum(providerRoles),
});

type InviteValues = z.infer<typeof inviteSchema>;

const membershipStatus: Record<string, string> = {
  invited: "Convidado",
  active: "Ativo",
  disabled: "Desativado",
};

export function ProviderAdmin() {
  const principal = useSessionStore((state) => state.principal);
  const enabled = isProviderAdmin(principal?.role) && principal?.mfaEnrollmentRequired !== true;
  const tenantId = principal?.tenantId ?? "sem-provedor";
  const memberships = useQuery({
    queryKey: queryKeys.memberships(tenantId),
    enabled,
    queryFn: listMemberships,
  });
  const sessions = useQuery({
    queryKey: queryKeys.sessions(tenantId),
    enabled,
    queryFn: listSessions,
  });

  if (!enabled || !principal) {
    return null;
  }
  if (memberships.isPending || sessions.isPending) {
    return (
      <p role="status" className="text-sm">
        Carregando.
      </p>
    );
  }
  if (memberships.isError || sessions.isError) {
    return (
      <p role="alert" className="text-sm text-destructive">
        {errorMessage(memberships.error ?? sessions.error)}
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-8">
      <InviteForm tenantId={tenantId} />
      <section aria-labelledby="usuarios-do-provedor" className="flex flex-col gap-3">
        <h2 id="usuarios-do-provedor" className="text-base font-medium">
          Usuários do provedor
        </h2>
        <ul aria-label="Usuários do provedor" className="flex flex-col gap-3">
          {memberships.data.map((member) => (
            <li
              key={member.id}
              className="flex flex-col gap-2 rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10"
            >
              <p>{member.user_id}</p>
              <p>
                {roleLabel(member.role)} · {membershipStatus[member.status] ?? "Situação desconhecida"}
              </p>
              <p>Desde {formatWhen(member.created_at)}</p>
              <MembershipAction membershipId={member.id} status={member.status} tenantId={tenantId} />
            </li>
          ))}
        </ul>
      </section>
      <section aria-labelledby="sessoes-do-provedor" className="flex flex-col gap-3">
        <h2 id="sessoes-do-provedor" className="text-base font-medium">
          Sessões
        </h2>
        <ul aria-label="Sessões do provedor" className="flex flex-col gap-3">
          {sessions.data.map((session) => (
            <li
              key={session.id}
              className="flex flex-col gap-2 rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10"
            >
              <p>{session.current ? "Esta sessão" : "Outra sessão"}</p>
              <p>Aberta em {formatWhen(session.created_at)}</p>
              <p>Encerra em {formatWhen(session.expires_at)}</p>
              <RevokeButton sessionId={session.id} current={session.current} tenantId={tenantId} />
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

function InviteForm({ tenantId }: { tenantId: string }) {
  const queryClient = useQueryClient();
  const form = useForm<InviteValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { email: "", role: "agent" },
  });
  const invite = useMutation({
    mutationFn: (values: InviteValues) => inviteMember(values.email, values.role),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.memberships(tenantId) });
      form.reset({ email: "", role: "agent" });
    },
  });

  return (
    <form
      noValidate
      className="flex max-w-md flex-col gap-3"
      onSubmit={form.handleSubmit((values) => {
        invite.mutate(values);
      })}
    >
      <h2 className="text-base font-medium">Convidar</h2>
      {invite.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(invite.error)}
        </p>
      ) : null}
      {invite.isSuccess ? (
        <p role="status" className="text-sm">
          Convite registrado. O token segue por e-mail e não aparece aqui.
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor="email-do-convite">E-mail</Label>
        <Input id="email-do-convite" type="email" autoComplete="off" {...form.register("email")} />
        {form.formState.errors.email?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.email.message}</p>
        ) : null}
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="papel-do-convite">Papel</Label>
        <select
          id="papel-do-convite"
          className="h-11 rounded-lg border border-input bg-transparent px-2.5 text-sm"
          {...form.register("role")}
        >
          {providerRoles.map((role) => (
            <option key={role} value={role}>
              {roleLabel(role)}
            </option>
          ))}
        </select>
      </div>
      <Button type="submit" disabled={invite.isPending} className="h-11 w-fit px-3">
        Convidar
      </Button>
    </form>
  );
}

function MembershipAction({
  membershipId,
  status,
  tenantId,
}: {
  membershipId: string;
  status: string;
  tenantId: string;
}) {
  const queryClient = useQueryClient();
  const active = status !== "disabled";
  const change = useMutation({
    mutationFn: () => setMembershipActive(membershipId, !active),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.memberships(tenantId) });
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
        {active ? "Desativar" : "Reativar"}
      </Button>
    </div>
  );
}

function RevokeButton({
  sessionId,
  current,
  tenantId,
}: {
  sessionId: string;
  current: boolean;
  tenantId: string;
}) {
  const queryClient = useQueryClient();
  const endSession = useEndSession();
  const revoke = useMutation({
    mutationFn: () => revokeSession(sessionId),
    onSuccess: async () => {
      if (current) {
        await endSession();
        return;
      }
      await queryClient.invalidateQueries({ queryKey: queryKeys.sessions(tenantId) });
    },
  });

  return (
    <div className="flex flex-col gap-2">
      {revoke.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {errorMessage(revoke.error)}
        </p>
      ) : null}
      <Button
        type="button"
        variant="outline"
        className="h-11 w-fit px-3"
        disabled={revoke.isPending}
        onClick={() => {
          revoke.mutate();
        }}
      >
        Derrubar sessão
      </Button>
    </div>
  );
}

