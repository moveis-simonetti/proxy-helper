; Instalador do MS Proxy.
;
; Inno Setup e não MSI: a implantação por Intune/GPO aceita um .exe com
; parâmetros silenciosos (/VERYSILENT) tão bem quanto um .msi, e um MSI
; exigiria WiX e um modelo de componentes que este produto — dois binários e
; um atalho — não tem o que preencher.
;
; A instalação é POR USUÁRIO (PrivilegesRequired=lowest), o que combina com
; o resto do produto: WinINET, as tarefas agendadas e a senha protegida são
; todos do usuário. Instalar por máquina pediria administrador sem que nada
; aqui precise dele.

#define AppName "MS Proxy"
; De onde vêm os binários. O build passa isto com /DSourceDir; sem ele o
; padrão aponta para dist/windows, que é onde scripts/build-windows.sh grava.
#ifndef SourceDir
  #define SourceDir "..\..\dist\windows"
#endif
#define AppVersion GetEnv("MSPROXY_VERSION")
#if AppVersion == ""
  #define AppVersion "0.0.0-dev"
#endif

[Setup]
AppId={{8F3A1C42-9E77-4D0B-8B21-6C4F2E5A9D13}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=Simonetti
DefaultDirName={autopf}\MSProxy
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
; Sem administrador: tudo que o produto escreve é do usuário.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputBaseFilename=MSProxy-Setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
; Sem isto o instalador sai com o ícone genérico do Inno Setup.
SetupIconFile=msproxy.ico
UninstallDisplayIcon={app}\MSProxy.exe
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"

[Files]
; A interface e o programa que mantém o proxy funcionando. Os dois ficam no
; mesmo diretório porque a interface procura o segundo ao lado de si mesma
; antes de olhar o PATH.
Source: "{#SourceDir}\MSProxy.exe";      DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\proxy-helper.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}";           Filename: "{app}\MSProxy.exe"
Name: "{userdesktop}\{#AppName}";     Filename: "{app}\MSProxy.exe"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Criar um atalho na área de trabalho"; Flags: unchecked

[Run]
; Abre o aplicativo ao final da instalação interativa. Numa implantação
; silenciosa isto não roda, e a pessoa encontra o MS Proxy no menu Iniciar.
Filename: "{app}\MSProxy.exe"; Description: "Abrir o {#AppName}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Antes de qualquer outra coisa: desfazer a configuração de proxy do Windows.
; Desinstalar com o proxy ligado deixaria o sistema apontando para
; 127.0.0.1:8888 depois que o programa que atende nessa porta some — a
; máquina fica sem internet e nada na tela explica por quê.
Filename: "{app}\proxy-helper.exe"; Parameters: "proxy unset"; Flags: runhidden; RunOnceId: "ClearProxy"
; Parar o serviço antes de apagar qualquer coisa. "schtasks /Delete" remove a
; tarefa mas não encerra o processo que ela já iniciou: sem isto o
; proxy-helper.exe continuaria vivo segurando a porta 8888 até o próximo
; reinício, e o Windows não deixa apagar um .exe em uso.
Filename: "schtasks"; Parameters: "/End /TN ""MS Proxy""";     Flags: runhidden; RunOnceId: "EndDaemonTask"
Filename: "taskkill"; Parameters: "/IM proxy-helper.exe /F";   Flags: runhidden; RunOnceId: "KillDaemon"
; As tarefas agendadas sobrevivem à remoção dos arquivos se ninguém as
; apagar, e uma tarefa apontando para um binário que não existe mais falha
; silenciosamente a cada logon.
Filename: "schtasks"; Parameters: "/Delete /TN ""MS Proxy"" /F";            Flags: runhidden; RunOnceId: "DelDaemonTask"
Filename: "schtasks"; Parameters: "/Delete /TN ""MS Proxy (bandeja)"" /F";  Flags: runhidden; RunOnceId: "DelTrayTask"

[UninstallDelete]
; O log do serviço.
Type: filesandordirs; Name: "{localappdata}\proxy-helper"
; Os perfis — e, dentro deles, a senha cifrada pelo Windows. Desinstalar
; apaga tudo: deixar a credencial no disco depois que o programa que a usa
; foi removido é pior do que obrigar quem reinstalar a configurar de novo.
Type: filesandordirs; Name: "{userappdata}\proxy-helper"
