import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { Link, useSearchParams } from "react-router";
import { z } from "zod";
import { confirmPasswordReset, requestPasswordReset } from "@/api/panel";
import { errorMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { emailSchema, passwordSchema } from "@/components/auth/password";

const requestSchema = z.object({ email: emailSchema });
const confirmSchema = z.object({
  token: z.string().trim().min(1, "Informe o token recebido.").max(128, "Use no máximo 128 caracteres."),
  password: passwordSchema,
});

type RequestValues = z.infer<typeof requestSchema>;
type ConfirmValues = z.infer<typeof confirmSchema>;

export function PasswordResetRequestForm() {
  const [done, setDone] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const form = useForm<RequestValues>({
    resolver: zodResolver(requestSchema),
    defaultValues: { email: "" },
  });

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (values) => {
        setServerError(null);
        setPending(true);
        try {
          await requestPasswordReset(values.email);
          setDone(true);
        } catch (error) {
          setServerError(errorMessage(error));
        } finally {
          setPending(false);
        }
      })}
    >
      <p className="text-sm text-muted-foreground">
        A resposta é a mesma quando o e-mail não existe.
      </p>
      {serverError ? (
        <p role="alert" className="text-sm text-destructive">
          {serverError}
        </p>
      ) : null}
      {done ? (
        <p role="status" className="text-sm">
          Se o e-mail existir, enviaremos as instruções.
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor="email-de-recuperacao">E-mail</Label>
        <Input id="email-de-recuperacao" type="email" autoComplete="username" {...form.register("email")} />
        {form.formState.errors.email?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.email.message}</p>
        ) : null}
      </div>
      <Button type="submit" disabled={pending} className="h-11 w-fit px-3">
        Enviar pedido
      </Button>
      <Link to="/recuperar-senha/confirmar" className="text-sm underline-offset-4 hover:underline">
        Já tenho o token
      </Link>
    </form>
  );
}

export function PasswordResetConfirmForm() {
  const [params] = useSearchParams();
  const [done, setDone] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const form = useForm<ConfirmValues>({
    resolver: zodResolver(confirmSchema),
    defaultValues: { token: params.get("token") ?? "", password: "" },
  });

  return (
    <form
      noValidate
      className="flex flex-col gap-4"
      onSubmit={form.handleSubmit(async (values) => {
        setServerError(null);
        setPending(true);
        try {
          await confirmPasswordReset({ token: values.token, password: values.password });
          setDone(true);
        } catch (error) {
          setServerError(errorMessage(error));
        } finally {
          setPending(false);
        }
      })}
    >
      {serverError ? (
        <p role="alert" className="text-sm text-destructive">
          {serverError}
        </p>
      ) : null}
      {done ? (
        <p role="status" className="text-sm">
          Senha alterada. As sessões anteriores foram encerradas.{" "}
          <Link to="/entrar" className="font-medium underline-offset-4 hover:underline">
            Entrar
          </Link>
        </p>
      ) : null}
      <div className="flex flex-col gap-2">
        <Label htmlFor="token-de-recuperacao">Token de recuperação</Label>
        <Input id="token-de-recuperacao" autoComplete="off" {...form.register("token")} />
        {form.formState.errors.token?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.token.message}</p>
        ) : null}
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="senha-nova">Nova senha</Label>
        <Input
          id="senha-nova"
          type="password"
          autoComplete="new-password"
          {...form.register("password")}
        />
        {form.formState.errors.password?.message ? (
          <p className="text-sm text-destructive">{form.formState.errors.password.message}</p>
        ) : null}
      </div>
      <Button type="submit" disabled={pending} className="h-11 w-fit px-3">
        Guardar senha
      </Button>
    </form>
  );
}
