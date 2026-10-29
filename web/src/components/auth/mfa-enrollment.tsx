import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { Link, useNavigate } from "react-router";
import { z } from "zod";
import { beginMfa, confirmMfa } from "@/api/panel";
import type { Enrollment } from "@/api/panel";
import { errorMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useEndSession } from "@/components/auth/use-end-session";
import { useSessionStore } from "@/stores/session-store";

const confirmSchema = z.object({
  code: z.string().trim().min(1, "Informe o código do aplicativo.").max(16, "Use no máximo 16 caracteres."),
});

type ConfirmValues = z.infer<typeof confirmSchema>;

export function MfaEnrollment() {
  const principal = useSessionStore((state) => state.principal);
  const completeEnrollment = useSessionStore((state) => state.completeEnrollment);
  const endSession = useEndSession();
  const navigate = useNavigate();
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [serverError, setServerError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const form = useForm<ConfirmValues>({
    resolver: zodResolver(confirmSchema),
    defaultValues: { code: "" },
  });

  if (!principal) {
    return (
      <p className="text-sm">
        Entre para cadastrar o autenticador.{" "}
        <Link to="/entrar" className="font-medium underline-offset-4 hover:underline">
          Entrar
        </Link>
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <p className="text-sm text-muted-foreground">
        O segredo e os códigos de recuperação aparecem uma única vez. Guarde os
        códigos fora deste navegador. Cada código funciona uma vez.
      </p>
      {serverError ? (
        <p role="alert" className="text-sm text-destructive">
          {serverError}
        </p>
      ) : null}
      <Button
        type="button"
        className="h-11 w-fit px-3"
        disabled={pending}
        onClick={() => {
          setServerError(null);
          setPending(true);
          void beginMfa()
            .then((created) => {
              setEnrollment(created);
            })
            .catch((error: unknown) => {
              setServerError(errorMessage(error));
            })
            .finally(() => {
              setPending(false);
            });
        }}
      >
        Gerar códigos de recuperação
      </Button>
      {enrollment ? (
        <div className="flex flex-col gap-3">
          <p className="text-sm">
            Segredo: <span className="font-mono">{enrollment.secret}</span>
          </p>
          <p className="text-sm break-all">Endereço do autenticador: {enrollment.otpauth_url}</p>
          <ul aria-label="Códigos de recuperação" className="flex flex-col gap-1">
            {(enrollment.recovery_codes ?? []).map((code) => (
              <li key={code} className="font-mono text-sm">
                {code}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <form
        noValidate
        className="flex flex-col gap-3"
        onSubmit={form.handleSubmit(async (values) => {
          setServerError(null);
          setPending(true);
          try {
            const state = await confirmMfa(values.code);
            if (state.mfa_enrollment_required) {
              setServerError("O cadastro ainda não foi confirmado.");
              return;
            }
            completeEnrollment();
            navigate("/painel", { replace: true });
          } catch (error) {
            setServerError(errorMessage(error));
          } finally {
            setPending(false);
          }
        })}
      >
        <div className="flex flex-col gap-2">
          <Label htmlFor="codigo-do-aplicativo">Código do aplicativo</Label>
          <Input
            id="codigo-do-aplicativo"
            autoComplete="one-time-code"
            aria-invalid={form.formState.errors.code ? true : undefined}
            {...form.register("code")}
          />
          {form.formState.errors.code?.message ? (
            <p className="text-sm text-destructive">{form.formState.errors.code.message}</p>
          ) : null}
        </div>
        <Button type="submit" disabled={pending} className="h-11 w-fit px-3">
          Confirmar código
        </Button>
      </form>
      <Button type="button" variant="outline" className="h-11 w-fit px-3" onClick={() => void endSession()}>
        Sair
      </Button>
    </div>
  );
}
