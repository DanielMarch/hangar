<?php
/**
 * sde.php — create the SDE lookup tables the eager-loads join against.
 *
 * These are NOT created by any migration: a real SeAT installation imports
 * them out of band with `php artisan seat:sde:update`, from CCP's static
 * data export. They are created here EMPTY, which is exactly the state of a
 * SeAT installation that has not yet run that import — a real, supported
 * state, and the one the corpus is deliberately recorded in.
 *
 * WHY EMPTY, AND WHAT IT COSTS. The eager-loaded `type` object is legacy's
 * denormalised Fuzzwork `invTypes` row: typeID, typeName, portionSize,
 * basePrice and the rest, in that column vocabulary. HANGAR's `sde.*`
 * schema is built from CCP's modern JSONL export and keeps a promoted
 * column set plus the raw row as jsonb (migration 00036), so it cannot
 * reproduce that object field-for-field for arbitrary data. Recording the
 * corpus with the SDE populated would therefore produce a target the shim
 * provably cannot match, and the gate would be measuring the wrong thing.
 * Recording it empty produces a target the shim CAN match exactly, and the
 * residual gap — eager-loaded SDE sub-objects on an SDE-populated legacy
 * installation — is written down in docs/APPENDIX_C_MIGRATION.md rather
 * than papered over.
 */

use Illuminate\Support\Facades\Schema;
use Illuminate\Database\Schema\Blueprint;

function createSdeTables(): void
{
    if (! Schema::hasTable('invTypes')) {
        Schema::create('invTypes', function (Blueprint $table) {
            $table->integer('typeID')->primary();
            $table->integer('groupID')->nullable();
            $table->string('typeName')->nullable();
            $table->text('description')->nullable();
            $table->double('mass')->nullable();
            $table->double('volume')->nullable();
            $table->double('capacity')->nullable();
            $table->integer('portionSize')->nullable();
            $table->integer('raceID')->nullable();
            $table->double('basePrice')->nullable();
            $table->boolean('published')->nullable();
            $table->integer('marketGroupID')->nullable();
            $table->integer('iconID')->nullable();
            $table->integer('soundID')->nullable();
            $table->integer('graphicID')->nullable();
        });
    }
    if (! Schema::hasTable('invGroups')) {
        Schema::create('invGroups', function (Blueprint $table) {
            $table->integer('groupID')->primary();
            $table->integer('categoryID')->nullable();
            $table->string('groupName')->nullable();
            $table->integer('iconID')->nullable();
            $table->boolean('useBasePrice')->nullable();
            $table->boolean('anchored')->nullable();
            $table->boolean('anchorable')->nullable();
            $table->boolean('fittableNonSingleton')->nullable();
            $table->boolean('published')->nullable();
        });
    }
    if (! Schema::hasTable('solar_systems')) {
        Schema::create('solar_systems', function (Blueprint $table) {
            $table->integer('system_id')->primary();
            $table->integer('constellation_id')->nullable();
            $table->string('name')->nullable();
            $table->double('security')->nullable();
        });
    }
    if (! Schema::hasTable('staStations')) {
        Schema::create('staStations', function (Blueprint $table) {
            $table->bigInteger('stationID')->primary();
            $table->string('stationName')->nullable();
            $table->integer('solarSystemID')->nullable();
        });
    }
    if (! Schema::hasTable('mapDenormalize')) {
        Schema::create('mapDenormalize', function (Blueprint $table) {
            $table->bigInteger('itemID')->primary();
            $table->string('itemName')->nullable();
            $table->integer('typeID')->nullable();
            $table->integer('solarSystemID')->nullable();
        });
    }
}
