package service

import (
	"fmt"
	"html"
	"strings"

	"api/internal/core/domain"
	"api/internal/core/port"
)

func firstName(u domain.User) string {
	if parts := strings.Fields(u.FullName); len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// mailLayout wraps the body in a minimal, mail-client-safe HTML shell
func mailLayout(title, body string) string {
	return `<!doctype html><html lang="es"><body style="margin:0;background:#f4f4f5;font-family:Arial,Helvetica,sans-serif;color:#18181b">` +
		`<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">` +
		`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border-radius:12px;padding:32px">` +
		`<tr><td><h1 style="margin:0 0 16px;font-size:20px">` + html.EscapeString(title) + `</h1>` + body +
		`<p style="margin:24px 0 0;font-size:12px;color:#71717a">Si no fuiste tú, ignora este correo: tu cuenta sigue segura.</p>` +
		`</td></tr></table></td></tr></table></body></html>`
}

// What each mailed code confirms, in the subject and the body
var mfaActions = map[string]string{
	domain.PurposeMFAEnable:   "activar la verificación en dos pasos",
	domain.PurposeMFADisable:  "desactivar la verificación en dos pasos",
	domain.PurposeMFARecovery: "generar nuevos códigos de respaldo",
}

func mfaCodeMail(u domain.User, code, purpose string) port.Mail {
	action := mfaActions[purpose]
	minutes := int(signInChallengeTTL.Minutes())
	return port.Mail{
		To:      u.Email,
		Subject: fmt.Sprintf("%s es tu código para %s", code, action),
		Text: fmt.Sprintf("Hola %s,\n\nTu código para %s en Joseph es: %s\n\nVence en %d minutos. Si no fuiste tú, cambia tu contraseña.\n",
			firstName(u), action, code, minutes),
		HTML: mailLayout("Confirma el cambio en tu cuenta",
			fmt.Sprintf(`<p style="margin:0 0 16px">Hola %s, usa este código para %s:</p>`+
				`<p style="margin:0 0 16px;font-size:32px;font-weight:bold;letter-spacing:8px">%s</p>`+
				`<p style="margin:0;color:#52525b">Vence en %d minutos. Si no fuiste tú, cambia tu contraseña.</p>`,
				html.EscapeString(firstName(u)), html.EscapeString(action), code, minutes)),
	}
}

func resetLinkMail(u domain.User, link string) port.Mail {
	return port.Mail{
		To:      u.Email,
		Subject: "Restablece tu contraseña de Joseph",
		Text: fmt.Sprintf("Hola %s,\n\nPara crear una nueva contraseña abre este enlace (vence en 1 hora):\n%s\n",
			firstName(u), link),
		HTML: mailLayout("Restablece tu contraseña",
			fmt.Sprintf(`<p style="margin:0 0 16px">Hola %s, recibimos un pedido para cambiar tu contraseña.</p>`+
				`<p style="margin:0 0 16px"><a href="%s" style="display:inline-block;background:#18181b;color:#ffffff;text-decoration:none;padding:12px 20px;border-radius:8px">Crear nueva contraseña</a></p>`+
				`<p style="margin:0;color:#52525b">El enlace vence en 1 hora y sirve una sola vez.</p>`,
				html.EscapeString(firstName(u)), html.EscapeString(link))),
	}
}
