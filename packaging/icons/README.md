# Ícones

Três arquivos, uma família. O SVG é a fonte: os tamanhos raster (16 a 256)
são gerados dele em `scripts/package-deb.sh`, não mantidos à parte.

| Arquivo | Onde aparece |
| --- | --- |
| `proxy-helper.svg` | menu, barra de tarefas, janela |
| `proxy-helper-symbolic.svg` | bandeja, proxy **ligado** |
| `proxy-helper-off-symbolic.svg` | bandeja, proxy **desligado** |

## Por que estes arquivos não têm comentários

**`<svg` precisa estar logo no começo do arquivo.** O gdk-pixbuf detecta o
formato farejando só um prefixo do conteúdo (~256 bytes na combinação
verificada: gdk-pixbuf 2.42.12, librsvg 2.60.0, GTK 3.24.50). Um preâmbulo
mais longo que isso — um cabeçalho de comentário, por exemplo — empurra o
`<svg` para fora dessa janela e o carregamento falha com
*"formato de arquivo de imagem não reconhecido"*.

Isso já aconteceu neste projeto: os três nasceram com um cabeçalho de
comentário explicando o desenho, e os três ficaram **invisíveis** —
o da bandeja não aparecia no painel e a entrada de menu do GNOME mostrava um
quadrado vazio. O modo de falha é cruel porque `IconTheme.HasIcon()` continua
devolvendo `true`: o tema *conhece* o arquivo, ninguém consegue *desenhá-lo*.

Confirmado por bissecção, mantendo o tamanho do arquivo constante e mudando
só a posição do `<svg`: byte 40 carrega, byte 528 falha. Tamanho não é o
critério — os ícones simbólicos do próprio sistema têm 2,4 KB e funcionam.

`TestIconsPutTheSvgTagFirst` (internal/gui/autostart_test.go) trava isso.

## Regras de desenho

- **Os simbólicos usam só `fill`**, nunca `stroke`: o painel recolore o
  ícone, e o stroke não acompanha de forma confiável.
- **A seta é um furo**, via `fill-rule="evenodd"`, não uma forma pintada por
  cima. A 16px, pintada por cima ela se funde ao escudo.
- **Ligado vs desligado = cheio vs contorno.** A 16px num painel é a única
  distinção que sobrevive; uma cópia esmaecida simplesmente some.
- **A seta é desproporcionalmente grande** de propósito. Uma proporcional
  virou borrão a 16px.
- **O colorido é a mesma geometria ×4**, num grid de 64px.

Tudo isso foi ajustado contra uma rasterização de 16px de verdade, não
olhando o SVG ampliado. Se editar, refaça essa conferência:

```
rsvg-convert -w 16 -h 16 -b '#2c2c2c' proxy-helper-symbolic.svg -o /tmp/t.png
```
