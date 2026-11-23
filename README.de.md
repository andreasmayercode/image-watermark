# image-watermark

[English version](README.md)

Unsichtbares Wasserzeichen im Frequenzraum für Bilder. Es übersteht Skalierung,
JPEG-Kompression, verzerrtes Skalieren, Drehen um 90°/180°, Spiegeln, leichtes
Weichzeichnen und – über ein Synchronisationsmuster – auch Zuschneiden, Drehen um
beliebige Winkel und das Einfügen in eine Collage. Ein einzelnes, statisch gelinktes Programm ohne Abhängigkeiten
(Linux x86-64/ARM64, macOS, Windows, ~6 MB) bettet ein, erkennt und stellt bei Bedarf
selbst die Prüf-Website bereit.

Download: [Releases](https://github.com/andreasmayercode/image-watermark/releases) –
je Plattform ein Archiv mit Programm, Prüf-Website (`web/`), Lizenz und Prüfsummen
(`SHA256SUMS`). Das Verfahren gibt es auch als Go-Bibliothek, und Anwendungen können darauf
ein eigenes Programm bauen (siehe „Go-Bibliothek“).

```
image-watermark embed  -in foto.jpg -out foto-wm.jpg -id 4711 -key-file watermark.key
image-watermark detect -in verdacht.jpg -key-file watermark.key
image-watermark serve  -listen 127.0.0.1:8080 -key-file watermark.key
```

## Verfahren (kurz)

1. Die Helligkeit (Luma) des Bildes wird auf ein festes Raster von 512 × 512
   umgerechnet, unabhängig von Größe und Seitenverhältnis. Skalierung ändert
   deshalb nur die Umrechnung, nicht die Lage des Wasserzeichens.
2. Auf diesem Raster wird die zweidimensionale DCT berechnet. Das Wasserzeichen
   liegt in einem Ring niedriger bis mittlerer Frequenzen (12–80 Schwingungen
   pro Bildbreite). Diese Frequenzen überstehen Verkleinern und JPEG.
3. Die ~19 600 Koeffizienten des Rings werden schlüsselabhängig auf 56 Plätze
   verteilt: 32 Bit Kennung, 16 Bit CRC, 8 Pilotbits. Jeder Koeffizient bekommt
   ein schlüsselabhängiges Vorzeichen (Spread Spectrum).
4. **Informierte Einbettung:** Je Platz wird nur die Differenz zwischen dem, was
   das Bild ohnehin „mitbringt“, und dem Zielwert ergänzt. Das Bild stört die
   Erkennung so kaum, und eine kleine Änderung genügt. Das Ergebnis wird am
   echten Bild nachgemessen und in bis zu fünf Durchgängen nachgeregelt.
5. **Wahrnehmungsmaske:** Die Änderung landet in strukturierten Bildbereichen
   (Laub, Stoff, Schnee, Kanten). Flache Flächen (Himmel, Haut, weißer
   Hintergrund) bleiben pixelgenau weitgehend unverändert.
6. **Erkennung:** gleiche Umrechnung und Korrelation in allen acht
   Ausrichtungen (gedreht, gespiegelt). Eine Kennung gilt als gefunden, wenn
   CRC-16 und alle acht Pilotbits stimmen und das Gesamtsignal die Schwelle
   erreicht. Bis zu zwei unsichere Bits werden per Soft-Decodierung korrigiert.

Nach dem Einbetten liest das Programm die Kennung selbst wieder aus
(`verified`) und warnt, wenn das nicht sicher gelingt.

### Synchronisationsmuster

Das 512er-Raster hängt am Bildrahmen. Schneidet jemand einen Rand ab, dreht das Bild um
einen freien Winkel oder setzt es in eine Collage, verschieben sich alle Träger – die
Kennung ist dann ohne Hilfe nicht mehr lesbar. Dafür legt das Programm zusätzlich ein
schwaches **Synchronisationsmuster** ins Bild:

1. Das Bild wird in 8 × 8 Kacheln geteilt (je Breite/8 × Höhe/8). Jede Kachel bekommt
   dasselbe schlüsselabhängige Muster aus 40 Sinusschwingungen mit 10–28 Schwingungen je
   Kachel – oberhalb des Bandes der Kennung, damit beide sich nicht stören. Das Muster wird
   zuerst eingebettet; die informierte Einbettung der Kennung gleicht es anschließend aus.
2. **Erkennung:** Liest `detect` die Kennung nicht direkt, sucht es das Muster. Die
   Wiederholung erzeugt im spektral geweißten Bild ein Gitter von Spitzen; die
   Autokorrelation liefert die beiden Gittervektoren (Drehung, Maßstab, Verzerrung).
   Spektrale Linien (Bildkanten, Collage-Ränder) werden vorher unterdrückt. Zusätzlich legt
   die Suche für achsparallele Gitter (Zuschnitt ohne Drehung) einen „Kamm“ über alle
   Kachelgrößen, die ein Zuschnitt bis 40 % je Achse erlaubt, und summiert die
   Spektralenergie genau an den 40 bekannten Musterfrequenzen. Das findet das Gitter auch
   dann, wenn eigene Regelmäßigkeiten des Bildes (Textzeilen, Bedienelemente) die
   Autokorrelation überdecken.
3. Das Bild wird entlang dieses Gitters zu einer einzigen Kachel gefaltet. Eine
   zyklische Korrelation mit dem bekannten Muster (FFT, acht Ausrichtungen) liefert
   Phase und Ausrichtung; das Gitter wird danach auf maximale Korrelation verfeinert.
   Ein Gitter gilt ab z ≥ 7 als gefunden.
4. Die lokale Korrelation je Kachel zeigt, welche Kacheln das Muster tragen; die
   besten 8 × 8-Fenster sind Kandidaten für den Originalrahmen. Für jeden wird das
   512er-Raster direkt aus dem verdächtigen Bild abgetastet (eine Kachel = 64 Rasterpunkte),
   abgeschnittene Teile werden weich aus dem Rand fortgesetzt, dann liest der normale
   Decoder (CRC, Pilotbits, Signalstärke).
5. Bei Collagen wird zusätzlich in Teilfenstern (2/3 und 1/2 der Bildgröße) gesucht,
   weil das eingefügte Bild dort einen größeren Anteil hat.

Das Ergebnis trägt dann `"synced": true`, die Stärke des Musters (`sync_confidence`)
und bei freien Winkeln `rotation_deg` (im Uhrzeigersinn). Kosten: rund 0,5 dB PSNR.
Eine Prüfung ohne Treffer dauert länger (1–3 s), weil alle Fenster durchsucht werden.

## Gemessene Robustheit

41 Fotos (1200–2240 px breit, darunter Panoramen und freigestellte Produkte),
Kennung 3141592653, Standardstärke, Version 1.0.0. „OK“ = Kennung korrekt gelesen;
in keinem Fall wurde eine falsche Kennung gelesen. Zwei sehr strukturarme Bilder
(`verified: false` schon beim Einbetten) sind mitgezählt.

| Veränderung des markierten Bildes | gelesen |
|---|---|
| unverändert (JPEG 85), JPEG 50 | 39/41 |
| auf 25 % verkleinert / auf 400 px Breite | 38/41, 39/41 |
| auf 300 px Breite | 35/41 |
| verzerrt skaliert (70 % × 50 %), 90° gedreht | 40/41 |
| Weichzeichner (auf ⅓ und zurück) | 39/41 |
| **2 % Rand abgeschnitten** | **37/41** |
| **ungleichmäßig beschnitten (10/3/17/8 %, ~26 % der Fläche)** | **31/41** |
| **um 3° gedreht** (Leinwand vergrößert) | **37/41** |
| um 17° gedreht und auf das Innere beschnitten | 17/41 |
| um −33° gedreht | 21/41 |
| um 45° gedreht und auf 50 % verkleinert | 20/41 |
| **in eine Collage eingefügt, 60 % der Breite** | **28/41** |
| in eine Collage eingefügt, 40 % der Breite | 12/41 |
| beschnitten (~30 % der Fläche) und auf 50 % verkleinert | 15/41 |
| Mittelausschnitt 50 % × 50 %, Collage mit 25 % der Breite | 0/41 |

Zweite Stichprobe: 54 Kamerafotos bis 16 MP und 6 Bildschirmfotos (PNG).

| Veränderung | 54 Fotos | 6 Bildschirmfotos |
|---|---|---|
| unverändert (JPEG 85) | 48/54 | 5/6 |
| 2 % Rand abgeschnitten | 48/54 | 3/6 |
| ungleichmäßig beschnitten (~26 % der Fläche) | 30/54 | 2/6 |
| um 3° / −33° gedreht | 46/54, 45/54 | 2/6, 2/6 |
| um 45° gedreht und auf 50 % verkleinert | 46/54 | 2/6 |
| um 17° gedreht und auf das Innere beschnitten | 4/54 | 1/6 |
| Collage 60 % / 40 % der Breite | 37/54, 23/54 | 2/6, 1/6 |

Die 6 nicht lesbaren Fotos sind sehr weiche Motive (Dunst, Gegenlicht, Sonnenuntergang):
Dort scheitert schon das Einbetten der Kennung (`verified: false`), das
Synchronisationsmuster allein hilft dann nicht. Bildschirmfotos mit viel Weiß und Text
tragen wenig Wasserzeichen; ein fast leeres Fenster (Textur 0,05) nimmt gar keins an.
Fehlalarme: alle 60 Bilder unverändert, beschnitten und gedreht mit fremdem Schlüssel –
kein Treffer (höchstes Signal 3,2).

Faustregel: Ein Rand von einigen Prozent, eine leichte Drehung und das Einfügen als
großer Teil einer Collage sind kein Problem. Je mehr Fläche fehlt, desto unsicherer
wird es: Die Kennung ist informiert eingebettet (sie gleicht das Bild selbst aus) und
braucht deshalb den größten Teil des Originalrahmens. Bei größeren Verlusten meldet
`detect` oft wenigstens `present_unreadable`.

Fehlalarme: 301 unmarkierte Bilder, kein einziger Treffer (höchstes Signal
3,4; zusätzlich müssen CRC-16 und acht Pilotbits stimmen). Mit
Synchronisationssuche: 41 unmarkierte Bilder je unverändert, beschnitten und gedreht,
kein Treffer (höchstes Signal 3,3). Mit falschem Schlüssel wird nichts erkannt.

Sichtbarkeit: Bei Fotos ist in der 1:1-Ansicht kein Unterschied zu erkennen
(Luma-Änderung RMS 3–4, PSNR 36–38 dB, konzentriert auf strukturierte
Bereiche). **Bei Grafiken und Screenshots** (große flache Flächen, Text) ist das
Wasserzeichen als leichte Schlieren an Text und Kanten sichtbar. Das Programm
warnt dann („large flat areas“).

### Kleine Bilder

Direkt in 640 × 400 px eingebettet (fünf Motive):

| Veränderung | Ergebnis |
|---|---|
| unverändert, JPEG 60, 75 % verkleinert, 400 px breit | 5/5 |
| auf 50 % (320 × 200) | 5/5 |
| auf 256 px Breite | 3/5 |
| verzerrt skaliert, 90° gedreht, weichgezeichnet, 200 % vergrößert | 5/5 |

Faustregel: Lesbar, solange das Bild etwa **300 px breit bzw. 200 px auf der
kurzen Seite** bleibt. Darunter wird es unsicher; direkt in 256 × 160
eingebettet meldet `embed` teils `verified: false`.

### Laufzeit und Speicher

Gemessen mit 1.0.0 auf 8 Kernen (ARM64):

| Bild | Einbetten | Erkennen direkt | Erkennen über Synchronisation (10 % beschnitten) | unmarkiert (alle Fenster) | Speicherspitze |
|---|---|---|---|---|---|
| 640 × 400 | 0,3 s | 0,13 s | 0,45 s | 1,1 s | 60 MB |
| 6000 × 4000 (24 MP) | 2,3 s | 0,4 s | 0,85 s | 2,6 s | 240 MB |

Das Programm ist ein eigener Prozess. Das `memory_limit` von PHP gilt dafür
nicht, wohl aber Grenzen des Hosters je Prozess oder je Konto (z. B. `ulimit`).
Den Speicher begrenzt bei Bedarf die Umgebungsvariable `GOMEMLIMIT=300MiB`.

## Grenzen

- **Großes Zuschneiden** (etwa ein Mittelausschnitt mit der halben Breite und Höhe)
  und kleine Collage-Anteile (unter etwa 40 % der Breite) überlebt die Kennung nicht,
  ebenso wenig starke Drehung mit Beschnitt in vielen Fällen. Das Synchronisationsmuster
  wird dann oft noch gefunden (`present_unreadable`), die Kennung braucht aber den
  größten Teil des Originalrahmens. Sehr breite Formate (Panoramen, Banner) haben flache
  Kacheln und sind weniger robust.
- Unter etwa 300 px Breite / 200 px kurzer Seite wird die Kennung
  unzuverlässig. Für Vorschaubilder mit deutlich anderem Zuschnitt (z. B. quadratisch
  aus Breitformat) das Wasserzeichen weiterhin nach dem Zuschneiden neu einbetten.
- Wer Schlüssel und Verfahren kennt, kann das Wasserzeichen gezielt entfernen.
  Der Schlüssel muss geheim bleiben. Ein Wasserzeichen ist ein Indiz für die
  Herkunft, kein kryptografischer Beweis.
- Mit einem neuen Schlüssel sind alte Wasserzeichen nicht mehr lesbar.
  Schlüssel daher nie wegwerfen. Mehrere Schlüssel = mehrere Prüfungen.
- 16-Bit-PNGs werden als 8 Bit gespeichert. CMYK-JPEGs werden nach RGB
  umgewandelt (das CMYK-Farbprofil entfällt). Ausgabe nur JPEG oder PNG.

## Installation (Linux)

```bash
tar -xzf image-watermark-1.0.0-linux-amd64.tar.gz
mkdir -p ~/watermark/bin ~/watermark/private
cp image-watermark-1.0.0-linux-amd64/image-watermark ~/watermark/bin/image-watermark
chmod 755 ~/watermark/bin/image-watermark
head -c 32 /dev/urandom | base64 > ~/watermark/private/watermark.key
chmod 600 ~/watermark/private/watermark.key
~/watermark/bin/image-watermark version
```

Der Schlüssel kommt per `-key-file` (empfohlen), über die Umgebungsvariable
`IMAGE_WM_KEY` oder per `-key` (dann in der Prozessliste sichtbar).

### Einbinden in die Bildbearbeitung

```bash
image-watermark embed -in upload.jpg -out upload.jpg -id 4711 \
    -key-file ~/watermark/private/watermark.key -json
```

- Das Wasserzeichen in das **ausgelieferte** Bild einbetten bzw. in das größte.
  Daraus abgeleitete kleinere Fassungen ab ~400 px behalten es.
- `-id` speichert eine Zahl von 0 bis 4 294 967 295 (32 Bit) – etwa eine Kunden-,
  Website- oder Fotografennummer aus der eigenen Verwaltung. Mehr Bits machten jedes Bit
  störanfälliger (nicht rechenintensiver). Programme auf Basis des Pakets `cli` können die
  Zahl als Text zeigen, z. B. als kurzen Code (siehe „Eigene Programme“).
- `-in`/`-out` dürfen dieselbe Datei sein (wird atomar ersetzt) oder `-` für
  stdin/stdout. Eingabe JPEG, PNG, GIF, WebP; Ausgabe nach Endung `.jpg`/`.png`.
- EXIF, ICC-Profil, XMP und IPTC bleiben bei JPEG erhalten.
- `-strength 1.5` macht es robuster (und eher sichtbar), `-quality 92` ist die
  JPEG-Qualität, `-no-mask` verteilt gleichmäßig (nur für Sonderfälle).
- `-namespace meine-app` bettet in einen eigenen Namensraum ein (Buchstaben, Ziffern, `.`,
  `_`, `-`; Standard `image-watermark`). Wasserzeichen eines Namensraums werden mit einem
  anderen nicht gefunden, `detect` und `serve` brauchen also dasselbe `-namespace`. So kann
  eine Anwendung das fertige Programm nutzen, statt ein eigenes zu bauen (siehe
  „Go-Bibliothek“).
- Exit-Code 0 = eingebettet. Bei `verified: false` (Bild zu strukturarm) steht
  eine Warnung auf stderr, ebenso bei schwachem Synchronisationsmuster
  (`sync_confidence` unter 7: Zuschnitt und Drehung dann eher nicht lesbar).

JSON-Ausgabe von `embed -json`:

```json
{"ok":true,"id":4711,"width":1920,"height":1280,"verified":true,"confidence":8.53,
 "texture":0.51,"delta_rms":3.18,"psnr_db":38.08,"sync_confidence":24.1}
```

### Erkennen

```bash
image-watermark detect -in bild.jpg -key-file … [-json] [-v]
```

Exit-Code 0 = gefunden, 1 = nicht gefunden, 2 = Fehler.

```json
{"status":"found","found":true,"id":4711,"confidence":8.99,"crc_ok":true,
 "corrected_bits":0,"min_bit_z":1.35,"orientation":"none","width":960,"height":640}
```

Über das Synchronisationsmuster gelesen (hier: Bild um 12° gedreht):

```json
{"status":"found","found":true,"id":4711,"confidence":8.42,"crc_ok":true,
 "corrected_bits":0,"min_bit_z":1.01,"orientation":"none","width":2146,"height":1654,
 "synced":true,"rotation_deg":12.01,"sync_confidence":24.29}
```

`orientation` ist immer einer der acht Namen (nächstes Vielfaches von 90°),
`rotation_deg` kommt bei freien Winkeln dazu. `status`: `found`, `present_unreadable`
(deutliches Signal, Kennung nicht lesbar) oder `none`. `confidence` ist die Signalstärke (unmarkierte Bilder
liegen meist unter 3, markierte zwischen 5 und 10).

## Prüf-Website

`web/index.html` ist die Upload-Seite (Deutsch/Englisch nach Browsersprache,
Hell/Dunkel). Sie schickt das Bild an `detect.php` im selben Verzeichnis.
Bilder werden nicht gespeichert.

**Variante A: PHP-Webspace.** `index.html` und `detect.php` in ein Verzeichnis
der Website kopieren, Binary und Schlüssel **außerhalb** des Document-Root. Die
Pfade stehen oben in `detect.php` (Standard: `../bin/image-watermark`,
`../private/watermark.key`) oder kommen aus den Umgebungsvariablen `IMAGE_WM_BINARY` und
`IMAGE_WM_KEY_FILE` (Apache: `SetEnv`); `IMAGE_WM_NAMESPACE` setzt `-namespace`, falls die
Bilder damit markiert wurden. PHP braucht `proc_open` (bei manchen
Shared-Hosting-Tarifen gesperrt, dann Variante B). `upload_max_filesize` und
`post_max_size` müssen zur Bildgröße passen.

**Variante B: eingebauter Server** (VPS/Root-Server). Das Binary liefert Seite
und Endpunkt selbst aus:

```bash
image-watermark serve -listen 127.0.0.1:8080 -key-file … -max-mb 30
```

Davor ein Reverse-Proxy mit TLS, z. B. Apache:

```apache
ProxyPass        /wasserzeichen/ http://127.0.0.1:8080/
ProxyPassReverse /wasserzeichen/ http://127.0.0.1:8080/
```

systemd-Unit (`/etc/systemd/system/image-watermark.service`):

```ini
[Unit]
Description=image-watermark detection website
After=network.target

[Service]
ExecStart=/opt/watermark/bin/image-watermark serve -listen 127.0.0.1:8080 -key-file /opt/watermark/private/watermark.key
User=www-data
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

Der Server verarbeitet höchstens zwei Bilder gleichzeitig, lehnt Bilder über
120 Megapixel ab und setzt eine restriktive Content-Security-Policy.
Eine Drosselung je IP gibt es nicht; die gehört bei Bedarf in den Proxy.

## Go-Bibliothek

```go
import watermark "github.com/andreasmayercode/image-watermark"

w, err := watermark.New(key, watermark.Options{Namespace: "meine-anwendung"})
src, err := watermark.Load("foto.jpg")
marked, info := w.Embed(src.Image, 4711, watermark.EmbedOptions{})
err = watermark.Save("foto-wm.jpg", marked, "jpeg", 92, src.Meta)

r := w.Detect(img, false) // r.Found, *r.ID, r.Confidence, r.Synced …
```

- `New` nimmt den geheimen Schlüssel (mindestens 8 Byte) und einen **Namensraum**. Der
  Namensraum gehört zum Datenformat: Wasserzeichen eines Namensraums werden mit einem
  anderen nicht gefunden, auch nicht mit demselben Schlüssel. Einmal je Anwendung wählen
  und nie mehr ändern. Leer = `image-watermark`.
- `Embed` ändert Bilder vom Typ `*image.YCbCr`, `*image.Gray` und `*image.NRGBA` direkt;
  `EmbedInfo.Verified = false` heißt: zu strukturarm, die Kennung lässt sich womöglich nicht
  sicher zurücklesen.
- `Load`/`Decode` lesen JPEG, PNG, GIF und WebP (64 × 64 px bis 120 Megapixel) und behalten
  die JPEG-Metadaten in `Source.Meta`; `Save`/`Encode` schreiben JPEG oder PNG.
- Ein `Watermarker` darf von mehreren Goroutinen gleichzeitig benutzt werden.

### Eigene Programme

Das Paket `cli` ist das vollständige Kommandozeilenprogramm (embed, detect, serve, version).
Eine Anwendung baut damit ihr eigenes Programm mit Name, Schlüssel-Variable, Namensraum,
Prüfseite und – auf Wunsch – einem `cli.Codec`, der die 32-Bit-Kennung als Text zeigt
(Schalter und JSON-Feld mit dem Namen des Codecs, z. B. `-code AM-NRD` / `"code":"AM-NRD"`):

```go
package main

import "github.com/andreasmayercode/image-watermark/cli"

func main() {
	cli.Main(cli.Config{
		Name: "mein-wasserzeichen", Version: "1.0.0", KeyEnv: "MEIN_WM_KEY",
		Namespace: "meine-anwendung", Codec: meinCodec{},
	})
}
```

## Bauen

Ein lokales Go wird nicht gebraucht. Gebaut wird im offiziellen Go-Container:

```bash
./build.sh          # dist/image-watermark-linux-amd64
./build.sh all      # zusätzlich linux-arm64 und darwin-arm64
```

Mit installiertem Go (Version laut `go.mod`) geht auch
`go install github.com/andreasmayercode/image-watermark/cmd/image-watermark@latest`.

Releases baut GitHub Actions: Ein Tag `vX.Y.Z`, das zur Version in
`cmd/image-watermark/main.go` passt,
erzeugt die Archive für Linux (amd64, arm64), macOS (arm64) und Windows (amd64) und hängt
sie an ein GitHub-Release.

Abhängigkeit: `golang.org/x/image` (nur WebP-Decoder). Das Datenformat der Kennung trägt
`{Namensraum}/1`, das Synchronisationsmuster `{Namensraum}/sync1` (Namensraum
`image-watermark`, sofern ein Programm keinen eigenen setzt, siehe „Go-Bibliothek“). Wer
Namensraum, Raster, Frequenzring, Bitaufteilung oder Kachelmuster ändert, macht bestehende
Wasserzeichen unlesbar.

Robustheit messen (Fotos in einem Verzeichnis, dauert einige Minuten):

```bash
WM_IMAGES=/pfad/zu/fotos WM_LIMIT=10 go test -run 'TestAttacks|TestFalsePositives' -v -timeout 60m
```

## Haftungsausschluss

image-watermark wird ohne jede Gewährleistung bereitgestellt, soweit das gesetzlich
zulässig ist (siehe Abschnitte 15 und 16 der `LICENSE`). Das gilt für den Quelltext ebenso
wie für die fertigen Programme (Binaries):

- Die Binaries werden von GitHub Actions automatisch aus dem getaggten Quelltext gebaut.
  Sie sind nicht signiert; macOS und Windows können deshalb beim ersten Start warnen.
  Prüfen Sie heruntergeladene Archive mit `SHA256SUMS`, oder bauen Sie das Programm selbst
  aus dem Quelltext (siehe „Bauen“).
- Sie führen das Programm auf eigene Verantwortung aus. Testen Sie es zuerst mit Kopien.
  Sind `-in` und `-out` dieselbe Datei, wird das Original ersetzt – bewahren Sie Originale
  gesondert auf.
- Ob Ihr Hosting-Vertrag das Ausführen eigener Programme erlaubt und welche Grenzen für
  Speicher und Rechenzeit gelten, regelt allein Ihr Anbieter. Prüfen Sie das vor der
  Installation. Die Beispiele in dieser Anleitung sind keine Empfehlung für einen
  bestimmten Anbieter oder Tarif.
- Ein gefundenes Wasserzeichen ist ein Indiz für die Herkunft, kein Beweis; ein nicht
  gefundenes beweist nicht, dass ein Bild nicht markiert war (siehe „Grenzen“). Die
  Ergebnisse sind keine Rechtsberatung.

## Lizenz

Copyright (C) 2026 the image-watermark authors. image-watermark ist freie Software unter der
GNU Affero General Public License, Version 3 oder neuer (`LICENSE`). Wer eine geänderte
Fassung als Netzdienst betreibt (z. B. die Prüf-Website per `serve`), muss deren Quelltext
den Nutzern anbieten – die Seite verlinkt dafür auf dieses Repository.

Die Binaries enthalten die Go-Standardbibliothek und `golang.org/x/image` (BSD-3-Clause),
siehe `THIRD-PARTY-NOTICES.md`.

## Rechte Dritter

Wenn Sie der Ansicht sind, dass image-watermark oder sein Einsatz Ihre Rechte verletzt,
melden Sie sich bitte zuerst über die [Issues](https://github.com/andreasmayercode/image-watermark/issues)
dieses Repositorys. Ich prüfe den Hinweis umgehend und ändere oder entferne den betroffenen Teil,
wo das nötig ist.
