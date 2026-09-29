(() => {
  const target = 'http://127.0.0.1:4040/webui/';
  const strings = {
    en: {
      status: 'Opening the local module interface…',
      fallback: 'The module interface did not open automatically. Make sure the Boot Animation Studio module server is running, then try again.',
      open: 'Open Module WebUI',
    },
    pt: {
      status: 'Abrindo a interface local do módulo…',
      fallback: 'A interface do módulo não abriu automaticamente. Verifique se o servidor do módulo Boot Animation Studio está em execução e tente novamente.',
      open: 'Abrir WebUI do módulo',
    },
    es: {
      status: 'Abriendo la interfaz local del módulo…',
      fallback: 'La interfaz del módulo no se abrió automáticamente. Comprueba que el servidor del módulo Boot Animation Studio esté en ejecución e inténtalo de nuevo.',
      open: 'Abrir WebUI del módulo',
    },
    fr: {
      status: 'Ouverture de l’interface locale du module…',
      fallback: 'L’interface du module ne s’est pas ouverte automatiquement. Vérifiez que le serveur du module Boot Animation Studio est actif, puis réessayez.',
      open: 'Ouvrir la WebUI du module',
    },
    de: {
      status: 'Lokale Moduloberfläche wird geöffnet…',
      fallback: 'Die Moduloberfläche wurde nicht automatisch geöffnet. Stelle sicher, dass der Server des Boot Animation Studio-Moduls läuft, und versuche es erneut.',
      open: 'Modul-WebUI öffnen',
    },
    it: {
      status: 'Apertura dell’interfaccia locale del modulo…',
      fallback: 'L’interfaccia del modulo non si è aperta automaticamente. Verifica che il server del modulo Boot Animation Studio sia in esecuzione e riprova.',
      open: 'Apri WebUI del modulo',
    },
    ja: {
      status: 'ローカルモジュール画面を開いています…',
      fallback: 'モジュール画面を自動的に開けませんでした。Boot Animation Studio モジュールサーバーが動作していることを確認して、もう一度お試しください。',
      open: 'モジュール WebUI を開く',
    },
    zh: {
      status: '正在打开本地模块界面…',
      fallback: '模块界面未自动打开。请确认 Boot Animation Studio 模块服务器正在运行，然后重试。',
      open: '打开模块 WebUI',
    },
  };
  const raw = (navigator.language || 'en').toLowerCase().split('-')[0];
  const supported = ['en', 'pt', 'es', 'fr', 'de', 'it', 'ja', 'zh'];
  const lang = supported.includes(raw) ? raw : 'en';
  const text = strings[lang];
  document.documentElement.lang = lang;
  const status = document.getElementById('launcher-status');
  const fallbackText = document.getElementById('launcher-fallback-text');
  const open = document.getElementById('launcher-open');
  if (status) status.textContent = text.status;
  if (fallbackText) fallbackText.textContent = text.fallback;
  if (open) open.textContent = text.open;

  const fallback = document.getElementById('fallback');
  window.setTimeout(() => {
    fallback?.classList.add('show');
  }, 900);

  window.location.replace(target);
})();
