package main

import (
	"crypto/sha256"
	"encoding/base64"
	"html"
	"net/http"
	"net/url"
	"strings"
)

// /trust is the HTTPS onboarding page for browsers on other devices (PC, console, tablet).
//
// A browser cannot show a certificate warning for a fetch() made by the Studio, so the
// Studio opens https://PHONE-IP:4041/trust in a new tab. The user accepts the browser's
// warning for this self-signed device certificate; this page then tells the Studio tab
// (window.opener) that the certificate is trusted and closes itself.
//
// The page is unauthenticated and reveals nothing about the device: no model, token,
// fingerprint or state. The only input is the Studio return URL, which must belong to
// an allowed Studio origin; anything else is dropped, so this is not an open redirect.

const trustLandingScript = `(function () {
  var root = document.getElementById('bas-trust');
  var origin = root.getAttribute('data-origin') || '';
  var back = root.getAttribute('data-return') || '';
  var text = {
    en: ['Secure connection ready', 'This browser now trusts your phone\'s Companion Module. This tab will close and Boot Animation Studio will connect.', 'If this tab stays open, go back to Boot Animation Studio.', 'Back to Boot Animation Studio'],
    pt: ['Conexão segura pronta', 'Este navegador agora confia no Companion Module do seu celular. Esta aba vai fechar e o Boot Animation Studio vai conectar.', 'Se esta aba continuar aberta, volte para o Boot Animation Studio.', 'Voltar ao Boot Animation Studio'],
    es: ['Conexión segura lista', 'Este navegador ahora confía en el Companion Module de tu teléfono. Esta pestaña se cerrará y Boot Animation Studio se conectará.', 'Si esta pestaña sigue abierta, vuelve a Boot Animation Studio.', 'Volver a Boot Animation Studio'],
    fr: ['Connexion sécurisée prête', 'Ce navigateur fait désormais confiance au Companion Module de votre téléphone. Cet onglet va se fermer et Boot Animation Studio va se connecter.', 'Si cet onglet reste ouvert, revenez à Boot Animation Studio.', 'Retour à Boot Animation Studio'],
    de: ['Sichere Verbindung bereit', 'Dieser Browser vertraut jetzt dem Companion Module deines Telefons. Dieser Tab wird geschlossen und Boot Animation Studio verbindet sich.', 'Falls dieser Tab offen bleibt, kehre zu Boot Animation Studio zurück.', 'Zurück zu Boot Animation Studio'],
    it: ['Connessione sicura pronta', 'Questo browser ora considera attendibile il Companion Module del tuo telefono. Questa scheda si chiuderà e Boot Animation Studio si connetterà.', 'Se questa scheda resta aperta, torna a Boot Animation Studio.', 'Torna a Boot Animation Studio'],
    ja: ['安全な接続の準備ができました', 'このブラウザはスマートフォンの Companion Module を信頼しました。このタブは閉じられ、Boot Animation Studio が接続します。', 'このタブが開いたままの場合は、Boot Animation Studio に戻ってください。', 'Boot Animation Studio に戻る'],
    zh: ['安全连接已就绪', '此浏览器现已信任你手机上的 Companion Module。此标签页将关闭，Boot Animation Studio 将自动连接。', '如果此标签页没有关闭，请返回 Boot Animation Studio。', '返回 Boot Animation Studio']
  };
  var lang = String(navigator.language || 'en').slice(0, 2).toLowerCase();
  var t = text[lang] || text.en;
  document.documentElement.lang = text[lang] ? lang : 'en';
  document.getElementById('bas-title').textContent = t[0];
  document.getElementById('bas-body').textContent = t[1];
  document.getElementById('bas-fallback').textContent = t[2];
  var link = document.getElementById('bas-back');
  if (back) { link.href = back; link.textContent = t[3]; } else { link.hidden = true; }
  if (origin && window.opener) {
    try { window.opener.postMessage({ type: 'bas-companion-trusted' }, origin); } catch (e) {}
    setTimeout(function () { window.close(); }, 800);
  }
})();`

const trustLandingStyle = `body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0f1115;color:#e8eaf0;font:16px/1.5 system-ui,sans-serif}main{max-width:30rem;padding:2rem 1.5rem;text-align:center}h1{font-size:1.35rem;margin:0 0 .75rem}p{margin:.5rem 0;color:#b7bcc9}a{display:inline-block;margin-top:1rem;color:#8ab4ff}`

var trustLandingCSP = func() string {
	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	return "default-src 'none'; script-src " + hash(trustLandingScript) + "; style-src " + hash(trustLandingStyle) +
		"; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}()

// validatedTrustReturn accepts only a Studio page on an allowed origin and returns the
// cleaned URL plus its origin. An empty result means "no return link, no postMessage".
func validatedTrustReturn(raw string) (string, string) {
	if raw == "" || len(raw) > 2048 {
		return "", ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", ""
	}
	origin := u.Scheme + "://" + u.Host
	if !isAllowedOrigin(origin) || isModuleWebUIOrigin(&url.URL{Scheme: u.Scheme, Host: u.Host}) {
		return "", ""
	}
	if u.Scheme == "http" && !isLoopbackHostname(u.Hostname()) {
		return "", ""
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), origin
}

func isLoopbackHostname(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func trustLandingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	returnURL, returnOrigin := validatedTrustReturn(r.URL.Query().Get("return"))
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", trustLandingCSP)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	page := strings.NewReplacer(
		"{{origin}}", html.EscapeString(returnOrigin),
		"{{return}}", html.EscapeString(returnURL),
		"{{style}}", trustLandingStyle,
		"{{script}}", trustLandingScript,
	).Replace(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Boot Animation Studio Companion</title><style>{{style}}</style></head>
<body><main id="bas-trust" data-origin="{{origin}}" data-return="{{return}}">
<h1 id="bas-title">Secure connection ready</h1>
<p id="bas-body">This browser now trusts your phone's Companion Module.</p>
<p id="bas-fallback">You can go back to Boot Animation Studio.</p>
<a id="bas-back" rel="noreferrer">Back to Boot Animation Studio</a>
</main><script>{{script}}</script></body></html>
`)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(page))
	}
}
