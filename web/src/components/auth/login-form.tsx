import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { Link, useNavigate } from "react-router";
import { z } from "zod";
import { login, rememberSession } from "@/api/panel";
import { MFA_CODE_REQUIRED, TENANT_REQUIRED, problemMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { emailSchema, passwordSchema } from "@/components/auth/password";
import { useSessionStore } from "@/stores/session-store";

const loginSchema = z.object({
  email: emailSchema,
  password: passwordSchema,
  tenantId: z.string().max(36, "Use no máximo 36 caracteres."),
  code: z.string().max(64, "Use no máximo 64 caracteres."),
});

type LoginValues = z.infer<typeof loginSchema>;

export function LoginForm() {
  const navigate = useNavigate();
  const endedReason = useSessionStore((state) => state.endedReason);
  const [serverError, setServerError] = useState<string | null>(null);
  const [needsCode, setNeedsCode] = useState(false);
  const [needsTenant, setNeedsTenant] = useState(false);
  const [pending, setPending] = useState(false);
  const form = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: "", password: "", tenantId: "", code: "" },
  });

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (values) => {
        setServerError(null);
        setPending(true);
        let result: Awaited<ReturnType<typeof login>>;
        try {
          result = await login({
            email: values.email,
            password: values.password,
            ...(values.tenantId.trim() ? { tenant_id: values.tenantId.trim() } : {}),
            ...(values.code.trim() ? { code: values.code.trim() } : {}),
          });
        } catch {
          setServerError("Não foi possível concluir. Tente de novo.");
          setPending(false);
          return;
        }
        setPending(false);
        if (!result.response.ok || !result.data) {
          const message = problemMessage(result.error, result.response.status);
          if (message === MFA_CODE_REQUIRED) {
            setNeedsCode(true);
            setServerError("Informe o código do aplicativo ou um código de recuperação.");
            return;
          }
          if (message === TENANT_REQUIRED) {
            setNeedsTenant(true);
            setServerError("Selecione o provedor desta conta.");
            return;
          }
          setServerError(message);
          return;
        }
        rememberSession(result.data);
        if (result.data.mfa_enrollment_required) {
          navigate("/mfa", { replace: true });
          return;
        }
        navigate("/painel", { replace: true });
      })}
    >
      {endedReason ? (
        <p role="status" className="text-sm text-foreground">
          Sua sessão expirou ou foi revogada. Entre novamente.
        </p>
      ) : null}
      {serverError ? (
        <p role="alert" className="text-sm text-destructive">
          {serverError}
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor="email-de-acesso">E-mail</Label>
        <Input
          id="email-de-acesso"
          type="email"
          autoComplete="username"
          aria-invalid={form.formState.errors.email ? true : undefined}
          {...form.register("email")}
        />
        {form.formState.errors.email?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.email.message}</p>
        ) : null}
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="senha-de-acesso">Senha</Label>
        <Input
          id="senha-de-acesso"
          type="password"
          autoComplete="current-password"
          aria-invalid={form.formState.errors.password ? true : undefined}
          {...form.register("password")}
        />
        {form.formState.errors.password?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.password.message}</p>
        ) : null}
      </div>
      {needsTenant ? (
        <div className="flex flex-col gap-2">
          <Label htmlFor="provedor-da-conta">Identificador do provedor</Label>
          <Input id="provedor-da-conta" autoComplete="off" {...form.register("tenantId")} />
        </div>
      ) : null}
      {needsCode ? (
        <div className="flex flex-col gap-2">
          <Label htmlFor="codigo-de-acesso">Código do aplicativo ou de recuperação</Label>
          <Input
            id="codigo-de-acesso"
            autoComplete="one-time-code"
            {...form.register("code")}
          />
        </div>
      ) : null}
      <Button type="submit" disabled={pending} className="h-11 w-fit px-3">
        Entrar
      </Button>
      <p className="flex flex-col gap-2 text-sm">
        <Link to="/recuperar-senha" className="underline-offset-4 hover:underline">
          Esqueci a senha
        </Link>
        <Link to="/convite" className="underline-offset-4 hover:underline">
          Recebi um convite
        </Link>
      </p>
    </form>
  );
}
