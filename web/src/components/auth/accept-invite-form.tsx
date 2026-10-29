import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { useNavigate, useSearchParams } from "react-router";
import { z } from "zod";
import { acceptInvitation, rememberSession } from "@/api/panel";
import { problemMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { passwordSchema } from "@/components/auth/password";

const inviteSchema = z.object({
  token: z.string().trim().min(1, "Informe o token do convite.").max(128, "Use no máximo 128 caracteres."),
  password: passwordSchema,
});

type InviteValues = z.infer<typeof inviteSchema>;

export function AcceptInviteForm() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [serverError, setServerError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const form = useForm<InviteValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { token: params.get("token") ?? "", password: "" },
  });

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (values) => {
        setServerError(null);
        setPending(true);
        let result: Awaited<ReturnType<typeof acceptInvitation>>;
        try {
          result = await acceptInvitation({ token: values.token, password: values.password });
        } catch {
          setServerError("Não foi possível concluir. Tente de novo.");
          setPending(false);
          return;
        }
        setPending(false);
        if (!result.response.ok || !result.data) {
          setServerError(problemMessage(result.error, result.response.status));
          return;
        }
        rememberSession(result.data);
        navigate(result.data.mfa_enrollment_required ? "/mfa" : "/painel", { replace: true });
      })}
    >
      {serverError ? (
        <p role="alert" className="text-sm text-destructive">
          {serverError}
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor="token-do-convite">Token do convite</Label>
        <Input id="token-do-convite" autoComplete="off" {...form.register("token")} />
        {form.formState.errors.token?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.token.message}</p>
        ) : null}
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="senha-inicial">Senha inicial</Label>
        <Input
          id="senha-inicial"
          type="password"
          autoComplete="new-password"
          {...form.register("password")}
        />
        {form.formState.errors.password?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.password.message}</p>
        ) : null}
      </div>
      <Button type="submit" disabled={pending} className="h-11 w-fit px-3">
        Criar acesso
      </Button>
    </form>
  );
}
