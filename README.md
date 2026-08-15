# joss_backup 2.1.0

Plugin oficial para la creación, verificación, cifrado AES-256, restauración y programación de respaldos en el lenguaje de programación Joss.

Empaquetado como un paquete binario `.jp` (JP v2) puro, portable, seguro y firmado criptográficamente con Ed25519. Se carga y registra automáticamente en cualquier aplicación Joss.

---

## 🚀 Instalación

```bash
joss pub add joss_backup
```

O agrégalo en tu `joss.yaml`:

```yaml
dependencies:
  joss_backup: "^2.1.0"
```

---

## ⚙️ Configuración (.env)

| Variable | Descripción | Valor por Defecto |
| --- | --- | --- |
| `BACKUP_PATH` | Directorio destino para almacenar los respaldos locales. | `storage/backups` |
| `APP_KEY` | Clave secreta predeterminada para el cifrado AES-256 si no se especifica una en `password()`. | Auto / `JWT_SECRET` |
| `DB` | Motor de base de datos (`mysql`, `sqlite`). | `sqlite` |

---

## 💻 Uso

### 1. Crear un Respaldo Completo con Cifrado AES-256:

```joss
$backup = new Backup()
$res = $backup->create()
    ->full()
    ->encrypt("clave-segura-123")
    ->keep(7)
    ->run()

($res["ok"]) ? {
    print("Respaldo creado: " . $res["backup_id"] . " (" . $res["size"] . " bytes)")
} : {
    print("Error creando respaldo: " . $res["error"])
}
```

### 2. Respaldo Solo de Archivos o Base de Datos:

```joss
// Solo archivos del proyecto
$resFiles = (new Backup())->create()->files()->run()

// Solo base de datos
$resDB = (new Backup())->create()->database()->run()
```

### 3. Listar Respaldos Existentes:

```joss
$backup = new Backup()
$list = $backup->list("local")

foreach ($list["backups"] as $item) {
    print("ID: " . $item["id"] . " | Fecha: " . $item["date"] . " | Tamaño: " . $item["size"] . " bytes")
}
```

### 4. Verificar Integridad de un Respaldo:

```joss
$backup = new Backup()
$resVerify = $backup->verify($backupId, "local", "clave-segura-123")

($resVerify["valid"]) ? {
    print("El archivo de respaldo es íntegro y válido.")
} : {
    print("Error en verificación: " . $resVerify["error"])
}
```

### 5. Restaurar un Respaldo:

```joss
$backup = new Backup()
$resRestore = $backup->restore($backupId)
    ->password("clave-segura-123")
    ->target("storage/restore")
    ->run()

($resRestore["ok"]) ? {
    print("Respaldo restaurado con éxito. Archivos: " . $resRestore["files_count"])
} : {
    print("Error restaurando: " . $resRestore["error"])
}
```

### 6. Eliminar un Respaldo:

```joss
$backup = new Backup()
$resDel = $backup->delete($backupId, "local")
```

---

## 🛠️ Compilación a Paquete .jp

```bash
joss plugin compile .
joss plugin inspect joss_backup.jp
```
