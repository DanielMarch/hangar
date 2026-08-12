<?php
/**
 * migrate.php — run eveseat's REAL migrations against MySQL 8, in filename
 * order across the three packages, so the resulting physical column order
 * is legacy's own rather than something reconstructed by hand.
 *
 * MySQL specifically, not SQLite: `->after()` and `->enum()` are MySQL
 * behaviours and column position after a drop/re-add differs between the
 * two engines. Field ORDER is exactly what Gate 7 measures, so the engine
 * has to be the one legacy ran on.
 */

use Illuminate\Database\Migrations\Migration;
use Illuminate\Support\Facades\Schema;

$container = require __DIR__ . '/bootstrap.php';

$dirs = [
    __DIR__ . '/../seat-services/src/database/migrations',
    __DIR__ . '/../seat-eveapi/src/database/migrations',
    __DIR__ . '/../seat-web/src/database/migrations',
];

$files = [];
foreach ($dirs as $dir) {
    foreach (glob($dir . '/*.php') as $file) {
        $files[basename($file)] = $file;
    }
}
ksort($files);

fwrite(STDERR, sprintf("migrate: %d migration files\n", count($files)));

Schema::disableForeignKeyConstraints();

// Several SeAT migrations are conditional on whether a LATER migration has
// already run (`Schema::hasTable('migrations') && DB::table('migrations')
// ->where(...)`), which a real `artisan migrate` always has available.
// Without it those migrations fatal and the tables they guard never reach
// their final shape.
Schema::dropIfExists('migrations');
$container['db']->connection()->statement(
    'CREATE TABLE migrations (id INT AUTO_INCREMENT PRIMARY KEY, migration VARCHAR(255) NOT NULL, batch INT NOT NULL)'
);

$ran = 0;
$failed = [];
foreach ($files as $name => $file) {
    try {
        $migration = require $file;
    } catch (\Throwable $e) {
        $failed[$name] = 'require: ' . $e->getMessage();
        continue;
    }
    if (! $migration instanceof Migration) {
        // Older SeAT migrations declare a class named after the file rather
        // than returning an anonymous class.
        $class = str_replace(' ', '', ucwords(str_replace('_', ' ',
            preg_replace('/^\d{4}_\d{2}_\d{2}_\d{6}_/', '', basename($file, '.php')))));
        if (! class_exists($class)) {
            $failed[$name] = 'no migration class (looked for ' . $class . ')';
            continue;
        }
        $migration = new $class();
    }
    try {
        $migration->up();
        $ran++;
    } catch (\Throwable $e) {
        $failed[$name] = get_class($e) . ': ' . $e->getMessage();
    }
    // Recorded whether or not up() threw: the conditional migrations above
    // ask "has this migration run", and a partially-applied one has.
    $container['db']->connection()->table('migrations')
        ->insert(['migration' => basename($file, '.php'), 'batch' => 1]);
}

Schema::enableForeignKeyConstraints();

fwrite(STDERR, sprintf("migrate: ran %d, failed %d\n", $ran, count($failed)));
foreach ($failed as $name => $why) {
    fwrite(STDERR, sprintf("  FAIL %s\n       %s\n", $name, substr($why, 0, 240)));
}
