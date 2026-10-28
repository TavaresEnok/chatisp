import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useSessionStore } from "@/stores/session-store";

const displayNameSchema = z.object({
  displayName: z
    .string()
    .trim()
    .min(2, "Informe pelo menos 2 caracteres.")
    .max(80, "Use no máximo 80 caracteres."),
});

type DisplayNameValues = z.infer<typeof displayNameSchema>;

export function LocalDisplayNameForm() {
  const displayName = useSessionStore((state) => state.displayName);
  const setDisplayName = useSessionStore((state) => state.setDisplayName);
  const form = useForm<DisplayNameValues>({
    resolver: zodResolver(displayNameSchema),
    defaultValues: { displayName },
  });
  const error = form.formState.errors.displayName;
  const currentValue = form.watch("displayName");
  const saved =
    displayName.length > 0 &&
    !form.formState.isDirty &&
    currentValue.trim() === displayName;
  const describedBy = error
    ? "nome-de-exibicao-ajuda nome-de-exibicao-erro"
    : "nome-de-exibicao-ajuda";

  return (
    <section aria-labelledby="preferencia-desta-aba" className="flex flex-col gap-3">
      <h2 id="preferencia-desta-aba" className="text-base font-medium">
        Preferência desta aba
      </h2>
      <form
        noValidate
        className="flex max-w-md flex-col gap-3"
        onSubmit={form.handleSubmit((values) => {
          setDisplayName(values.displayName);
          form.reset(values);
        })}
      >
        <div className="flex flex-col gap-2">
          <Label htmlFor="nome-de-exibicao">Nome de exibição nesta aba</Label>
          <Input
            id="nome-de-exibicao"
            autoComplete="name"
            aria-invalid={error ? true : undefined}
            aria-describedby={describedBy}
            {...form.register("displayName")}
          />
          <p id="nome-de-exibicao-ajuda" className="text-sm text-muted-foreground">
            Fica só na memória desta aba. Não cria usuário nem vai para o
            servidor.
          </p>
          {error?.message ? (
            <p id="nome-de-exibicao-erro" role="alert" className="text-sm text-destructive">
              {error.message}
            </p>
          ) : null}
        </div>
        <Button type="submit" className="h-11 w-fit px-3">
          Guardar nesta aba
        </Button>
        {saved ? (
          <p role="status" className="text-sm text-foreground">
            Nome guardado nesta aba.
          </p>
        ) : null}
      </form>
    </section>
  );
}
