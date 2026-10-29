import { z } from "zod";

export const passwordSchema = z
  .string()
  .min(10, "A senha precisa ter de 10 a 128 caracteres.")
  .max(128, "A senha precisa ter de 10 a 128 caracteres.")
  .refine((value) => value.trim().length > 0, "A senha precisa ter de 10 a 128 caracteres.");

export const emailSchema = z
  .string()
  .trim()
  .max(320, "Use no máximo 320 caracteres.")
  .refine(
    (value) => value.includes("@") && !value.includes(" ") && value.length >= 3,
    "Informe um e-mail válido.",
  );
