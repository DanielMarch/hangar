<?php
/**
 * bootstrap.php — a minimal but REAL Laravel 10 container, so the recorder
 * runs eveseat's own migrations, models and API Resource classes rather
 * than a reimplementation of them.
 */

use Illuminate\Container\Container;
use Illuminate\Database\Capsule\Manager as Capsule;
use Illuminate\Events\Dispatcher;
use Illuminate\Filesystem\Filesystem;
use Illuminate\Support\Facades\Facade;
use Illuminate\Config\Repository as ConfigRepository;
use Illuminate\Http\Request;

require __DIR__ . '/../vendor/autoload.php';

$container = new Container();
Container::setInstance($container);
Facade::setFacadeApplication($container);

$container->instance('config', new ConfigRepository([
    'database' => [
        'default' => 'mysql',
        'connections' => [
            'mysql' => [
                'driver' => 'mysql',
                'host' => getenv('DB_HOST') ?: 'corpus-mysql',
                'port' => 3306,
                'database' => 'seat',
                'username' => 'root',
                'password' => 'root',
                'charset' => 'utf8mb4',
                'collation' => 'utf8mb4_unicode_ci',
                'prefix' => '',
                'strict' => false,
                'engine' => 'InnoDB',
            ],
        ],
        'migrations' => 'migrations',
    ],
    'app' => ['key' => 'base64:' . base64_encode(str_repeat('a', 32)), 'cipher' => 'AES-256-CBC'],
    // eveapi/web migrations and models occasionally read settings; an empty
    // bag is enough because the recorder never exercises those paths.
    'seat' => [],
]));

// Capsule's own setup forces database.default = 'default', so the
// connection has to be registered under that name as well as 'mysql'.
$capsule = new Capsule($container);
$capsule->addConnection($container['config']['database.connections.mysql'], 'default');
$capsule->addConnection($container['config']['database.connections.mysql'], 'mysql');
$capsule->setEventDispatcher(new Dispatcher($container));
$capsule->setAsGlobal();
$capsule->bootEloquent();

$container->instance('db', $capsule->getDatabaseManager());
$container->instance('db.schema', $capsule->getConnection()->getSchemaBuilder());
$container->instance('files', new Filesystem());
$container->instance('request', Request::create('http://seat.local/api/v2/', 'GET'));
// SeAT's withDefault() closures call trans()/trans_choice() for the
// "unknown entity" placeholders, and those strings land IN the recorded
// bytes (`"name":"Unknown"` on an unresolved contract issuer, for example).
// So this is Laravel's REAL translator over eveseat/web's REAL `en`
// language files, not a stub returning the key — a stub would have recorded
// "web::seat.unknown" as the entity name and the corpus would be asserting
// a string legacy never emits.
$langLoader = new \Illuminate\Translation\FileLoader(new Filesystem(), __DIR__ . '/../seat-web/src/resources/lang');
$langLoader->addNamespace('web', __DIR__ . '/../seat-web/src/resources/lang');
$langLoader->addNamespace('services', __DIR__ . '/../seat-services/src/resources/lang');
$container->singleton('translator', function () use ($langLoader) {
    $translator = new \Illuminate\Translation\Translator($langLoader, 'en');
    $translator->setFallback('en');
    return $translator;
});

// Seat\Web\Http\Scopes\SquadScope is a global scope on the Squad model and
// its first line is `if (! auth()->check()) return $builder;`.
//
// Returning false here is FAITHFUL, not a convenience: legacy's /api/v2
// routes authenticate through the `api.auth` middleware, which validates an
// X-Token header against api_tokens and never logs a Laravel user in. So on
// a real /api/v2/squads request auth()->check() is false and the scope is a
// no-op — which is why the legacy endpoint returns classified squads to any
// valid token. Making the recorder "log in" would record a DIFFERENT
// response from the one legacy actually serves.
$guard = new class {
    public function check() { return false; }
    public function guest() { return true; }
    public function user() { return null; }
    public function id() { return null; }
    public function guard($name = null) { return $this; }
    public function shouldUse($name) {}
};
$container->instance('auth', $guard);
$container->instance(\Illuminate\Contracts\Auth\Factory::class, $guard);

// eveseat/services' Profile helper (which UserResource's `email` accessor
// reaches through) memoises settings with Cache::rememberForever. A real
// array-backed repository rather than a stub, so the caching semantics are
// Laravel's own; it starts empty on every run, which keeps the corpus a
// pure function of the fixtures.
$container->singleton('cache', fn () => new \Illuminate\Cache\Repository(new \Illuminate\Cache\ArrayStore()));
$container->singleton('cache.store', fn ($c) => $c['cache']);

// JsonResource::toResponse() goes through response()->json(), which
// resolves Illuminate\Contracts\Routing\ResponseFactory. The real
// ResponseFactory needs a view factory and a redirector it never touches on
// the json() path, so both are stubs — what matters is that json() is
// Laravel's own, because its encoding options are part of the bytes.
$container->singleton(\Illuminate\Routing\UrlGenerator::class, fn ($c) =>
    new \Illuminate\Routing\UrlGenerator(new \Illuminate\Routing\RouteCollection(), $c['request']));
$container->singleton(\Illuminate\Contracts\View\Factory::class, fn () => new class implements \Illuminate\Contracts\View\Factory {
    public function exists($view) { return false; }
    public function file($path, $data = [], $mergeData = []) { throw new \RuntimeException('views unused'); }
    public function make($view, $data = [], $mergeData = []) { throw new \RuntimeException('views unused'); }
    public function share($key, $value = null) { return $value; }
    public function composer($views, $callback) { return []; }
    public function creator($views, $callback) { return []; }
    public function addNamespace($namespace, $hints) { return $this; }
    public function replaceNamespace($namespace, $hints) { return $this; }
});
$container->singleton(\Illuminate\Routing\Redirector::class, fn ($c) =>
    new \Illuminate\Routing\Redirector($c[\Illuminate\Routing\UrlGenerator::class]));
$container->singleton(\Illuminate\Contracts\Routing\ResponseFactory::class, fn ($c) =>
    new \Illuminate\Routing\ResponseFactory(
        $c[\Illuminate\Contracts\View\Factory::class],
        $c[\Illuminate\Routing\Redirector::class]));

// SeAT's older migrations use the unqualified facade aliases a full
// Laravel app registers (`Schema::`, `DB::`). Without these, 396 of the 472
// migration files fail with "Class Schema not found" and the schema this
// recorder depends on never gets built.
foreach ([
    'Schema' => \Illuminate\Support\Facades\Schema::class,
    'DB' => \Illuminate\Support\Facades\DB::class,
    'Config' => \Illuminate\Support\Facades\Config::class,
    'Artisan' => \Illuminate\Support\Facades\Artisan::class,
    'Cache' => \Illuminate\Support\Facades\Cache::class,
    'Log' => \Illuminate\Support\Facades\Log::class,
] as $alias => $class) {
    if (! class_exists($alias, false)) {
        class_alias($class, $alias);
    }
}

// eveseat/services' global helpers a handful of migrations call. The
// recorder never exercises the settings system, so these are inert — they
// exist only so a migration that reads a setting does not fatal partway
// through building the schema.
if (! function_exists('setting')) {
    function setting($name, $global = false) { return null; }
}
if (! function_exists('human_diff')) {
    function human_diff($time) { return (string) $time; }
}
if (! function_exists('carbon')) {
    function carbon($time = null, $tz = null) {
        return $time === null ? \Carbon\Carbon::now($tz) : \Carbon\Carbon::parse($time, $tz);
    }
}

return $container;
