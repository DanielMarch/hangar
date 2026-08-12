<?php
/**
 * record.php — renders each legacy /api/v2 read route with the REAL
 * eveseat Resource classes over the REAL eveapi schema, and writes the
 * response bytes to out/.
 *
 * The query for each route is transcribed from the controller method (the
 * same ->with(), ->paginate() and ->appends() calls) rather than the
 * controller being invoked directly, because the controllers reach for
 * request()->validate() and the Filterable trait, neither of which is
 * exercised by this corpus: the shim does not implement OData `$filter`,
 * and a corpus recorded WITH a filter would be asserting behaviour the
 * shim deliberately refuses. Everything downstream of the query — resource
 * transformation, pagination envelope, JSON encoding — is Laravel's own
 * code, unmodified.
 */

use Illuminate\Http\Request;
use Illuminate\Pagination\Paginator;
use Illuminate\Support\Facades\DB;

use Seat\Api\Http\Resources\CharacterSheetResource;
use Seat\Api\Http\Resources\ContactResource;
use Seat\Api\Http\Resources\ContractResource;
use Seat\Api\Http\Resources\CorporationHistoryResource;
use Seat\Api\Http\Resources\CorporationSheetResource;
use Seat\Api\Http\Resources\IndustryResource;
use Seat\Api\Http\Resources\Json\JsonResource;
use Seat\Api\Http\Resources\JumpCloneResource;
use Seat\Api\Http\Resources\KillmailDetailResource;
use Seat\Api\Http\Resources\MailResource;
use Seat\Api\Http\Resources\MemberTrackingResource;
use Seat\Api\Http\Resources\NotificationResource;
use Seat\Api\Http\Resources\SquadResource;
use Seat\Api\Http\Resources\UserResource;

use Seat\Eveapi\Models\Assets\CharacterAsset;
use Seat\Eveapi\Models\Assets\CorporationAsset;
use Seat\Eveapi\Models\Character\CharacterCorporationHistory;
use Seat\Eveapi\Models\Character\CharacterInfo;
use Seat\Eveapi\Models\Character\CharacterNotification;
use Seat\Eveapi\Models\Character\CharacterSkill;
use Seat\Eveapi\Models\Clones\CharacterJumpClone;
use Seat\Eveapi\Models\Contacts\AllianceContact;
use Seat\Eveapi\Models\Contacts\CharacterContact;
use Seat\Eveapi\Models\Contacts\CorporationContact;
use Seat\Eveapi\Models\Contracts\CharacterContract;
use Seat\Eveapi\Models\Contracts\CorporationContract;
use Seat\Eveapi\Models\Corporation\CorporationInfo;
use Seat\Eveapi\Models\Corporation\CorporationMemberTracking;
use Seat\Eveapi\Models\Corporation\CorporationStructure;
use Seat\Eveapi\Models\Industry\CharacterIndustryJob;
use Seat\Eveapi\Models\Industry\CorporationIndustryJob;
use Seat\Eveapi\Models\Killmails\Killmail;
use Seat\Eveapi\Models\Killmails\KillmailDetail;
use Seat\Eveapi\Models\Mail\MailHeader;
use Seat\Eveapi\Models\Market\CharacterOrder;
use Seat\Eveapi\Models\Market\CorporationOrder;
use Seat\Eveapi\Models\Skills\CharacterSkillQueue;
use Seat\Eveapi\Models\Wallet\CharacterWalletJournal;
use Seat\Eveapi\Models\Wallet\CharacterWalletTransaction;
use Seat\Eveapi\Models\Wallet\CorporationWalletJournal;
use Seat\Eveapi\Models\Wallet\CorporationWalletTransaction;

use Seat\Web\Models\Squads\Squad;
use Seat\Web\Models\User;

$container = require __DIR__ . '/bootstrap.php';
require __DIR__ . '/fixtures.php';
require __DIR__ . '/sde.php';

// eveapi's CharacterInfo constructor resolves the application's user model
// from config('auth.providers.users.model'); without it, CharacterInfo::
// user() — which CharacterSheetResource reads for `user_id` — cannot build
// its hasOneThrough.
$container['config']->set('auth.providers.users.model', User::class);

const BASE = 'http://seat.local';
const OUT = __DIR__ . '/../out';

@mkdir(OUT, 0o777, true);
foreach (glob(OUT . '/*.json') as $stale) {
    unlink($stale);
}

// Fixtures are re-seeded from empty on every run so the corpus is a pure
// function of this file plus the pinned sources.
DB::statement('SET FOREIGN_KEY_CHECKS=0');
foreach (DB::select('SHOW TABLES') as $row) {
    $table = array_values((array) $row)[0];
    if ($table !== 'migrations') {
        DB::statement("TRUNCATE TABLE `$table`");
    }
}
DB::statement('SET FOREIGN_KEY_CHECKS=1');
createSdeTables();
seedFixtures();

$manifest = [];

/**
 * record renders one route and writes its bytes.
 *
 * $path is the legacy URL. $make receives the current Request and returns
 * whatever the controller returns (a resource or a resource collection).
 */
function record(string $name, string $path, callable $make, array $query = []): void
{
    global $container, $manifest;

    $url = BASE . $path . ($query ? '?' . http_build_query($query) : '');
    $request = Request::create($url, 'GET');
    $request->headers->set('Accept', 'application/json');
    $container->instance('request', $request);
    Paginator::currentPathResolver(fn () => BASE . $path);
    Paginator::currentPageResolver(fn () => (int) ($query['page'] ?? 1));

    $resource = $make($request);
    $response = $resource->toResponse($request);
    $body = $response->getContent();

    // Pretty-printing would change the bytes, so the corpus stores exactly
    // what Laravel put on the wire.
    file_put_contents(OUT . '/' . $name . '.json', $body);

    $manifest[] = [
        'name' => $name,
        'path' => $path,
        'query' => $query,
        'status' => $response->getStatusCode(),
        'bytes' => strlen($body),
        'sha256' => hash('sha256', $body),
    ];
    fwrite(STDERR, sprintf("  %-46s %4d  %6d bytes\n", $name, $response->getStatusCode(), strlen($body)));
}

$CHAR = 90000001;
$CORP = 98000001;
$ALLI = 99000001;

// ── AllianceController ───────────────────────────────────────────────────
record('alliance.contacts', "/api/v2/alliance/contacts/$ALLI", fn ($r) =>
    ContactResource::collection(
        AllianceContact::with('labels')->where('alliance_id', $ALLI)
            ->paginate()->appends($r->except('page'))));

// ── CharacterController ──────────────────────────────────────────────────
record('character.assets', "/api/v2/character/assets/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterAsset::with('type')->where('character_id', $CHAR)->paginate()));

record('character.contacts', "/api/v2/character/contacts/$CHAR", fn ($r) =>
    ContactResource::collection(
        CharacterContact::where('character_id', $CHAR)->paginate()->appends($r->except('page'))));

record('character.contracts', "/api/v2/character/contracts/$CHAR", fn ($r) =>
    ContractResource::collection(
        CharacterContract::with('detail', 'detail.acceptor', 'detail.assignee', 'detail.issuer',
            'detail.bids', 'detail.lines', 'detail.start_location', 'detail.end_location')
            ->where('character_id', $CHAR)
            ->paginate()->appends($r->except('page'))));

record('character.corporation-history', "/api/v2/character/corporation-history/$CHAR", fn ($r) =>
    CorporationHistoryResource::collection(
        CharacterCorporationHistory::where('character_id', $CHAR)->paginate()->appends($r->except('page'))));

record('character.industry', "/api/v2/character/industry/$CHAR", fn ($r) =>
    IndustryResource::collection(
        CharacterIndustryJob::where('character_id', $CHAR)->paginate()->appends($r->except('page'))));

record('character.jump-clones', "/api/v2/character/jump-clones/$CHAR", fn ($r) =>
    JumpCloneResource::collection(
        CharacterJumpClone::where('character_id', $CHAR)->paginate()));

record('character.mail', "/api/v2/character/mail/$CHAR", fn ($r) =>
    MailResource::collection(
        MailHeader::with('sender', 'body', 'recipients', 'recipients.entity')
            ->where(function ($q) use ($CHAR) {
                $q->whereHas('recipients', fn ($qq) => $qq->where('recipient_id', $CHAR))
                  ->orWhere('from', $CHAR);
            })->paginate()->appends($r->except('page'))));

record('character.market-orders', "/api/v2/character/market-orders/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterOrder::with('type')->where('character_id', $CHAR)->paginate()));

record('character.notifications', "/api/v2/character/notifications/$CHAR", fn ($r) =>
    NotificationResource::collection(
        CharacterNotification::where('character_id', $CHAR)->paginate()->appends($r->except('page'))));

record('character.sheet', "/api/v2/character/sheet/$CHAR", fn ($r) =>
    new CharacterSheetResource(
        CharacterInfo::with('affiliation.corporation', 'affiliation.alliance', 'affiliation.faction',
            'balance', 'skillpoints')->findOrFail($CHAR)));

record('character.skills', "/api/v2/character/skills/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterSkill::with('type')->where('character_id', $CHAR)->paginate()));

record('character.skill-queue', "/api/v2/character/skill-queue/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterSkillQueue::with('type')->where('character_id', $CHAR)->paginate()));

// Two pages recorded: the pagination envelope's interesting values
// (prev/next non-null, from/to, last_page) only appear when there is more
// than one page, and the shim has to synthesise all of them from keyset
// cursors.
record('character.wallet-journal', "/api/v2/character/wallet-journal/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterWalletJournal::with('first_party', 'second_party')->where('character_id', $CHAR)->paginate()));

record('character.wallet-journal.page2', "/api/v2/character/wallet-journal/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterWalletJournal::with('first_party', 'second_party')->where('character_id', $CHAR)->paginate()),
    ['page' => 2]);

record('character.wallet-transactions', "/api/v2/character/wallet-transactions/$CHAR", fn ($r) =>
    JsonResource::collection(
        CharacterWalletTransaction::with('party', 'type')->where('character_id', $CHAR)->paginate()));

// An EMPTY collection. Phase 18 found that an empty success is easy to
// mistake for a failure; the shim is exactly where that mistake would be
// silent, so the corpus pins what legacy emitted for "no rows": data is
// `[]`, never `null`, and the envelope is still present.
record('character.assets.empty', '/api/v2/character/assets/90000099', fn ($r) =>
    JsonResource::collection(
        CharacterAsset::with('type')->where('character_id', 90000099)->paginate()));

// ── CorporationController ────────────────────────────────────────────────
record('corporation.assets', "/api/v2/corporation/assets/$CORP", fn ($r) =>
    JsonResource::collection(
        CorporationAsset::with('type')->where('corporation_id', $CORP)->paginate()));

record('corporation.contacts', "/api/v2/corporation/contacts/$CORP", fn ($r) =>
    ContactResource::collection(
        CorporationContact::with('labels')->where('corporation_id', $CORP)->paginate()->appends($r->except('page'))));

record('corporation.contracts', "/api/v2/corporation/contracts/$CORP", fn ($r) =>
    ContractResource::collection(
        CorporationContract::with('detail', 'detail.acceptor', 'detail.assignee', 'detail.issuer',
            'detail.bids', 'detail.lines', 'detail.start_location', 'detail.end_location')
            ->where('corporation_id', $CORP)->paginate()->appends($r->except('page'))));

record('corporation.industry', "/api/v2/corporation/industry/$CORP", fn ($r) =>
    IndustryResource::collection(
        CorporationIndustryJob::with('blueprint', 'product')->where('corporation_id', $CORP)
            ->paginate()->appends($r->except('page'))));

record('corporation.market-orders', "/api/v2/corporation/market-orders/$CORP", fn ($r) =>
    JsonResource::collection(
        CorporationOrder::with('type')->where('corporation_id', $CORP)->paginate()));

record('corporation.member-tracking', "/api/v2/corporation/member-tracking/$CORP", fn ($r) =>
    MemberTrackingResource::collection(
        CorporationMemberTracking::with('ship')->where('corporation_id', $CORP)->paginate()->appends($r->except('page'))));

record('corporation.sheet', "/api/v2/corporation/sheet/$CORP", fn ($r) =>
    new CorporationSheetResource(
        CorporationInfo::with('ceo', 'creator', 'alliance', 'faction')->findOrFail($CORP)));

record('corporation.structures', "/api/v2/corporation/structures/$CORP", fn ($r) =>
    JsonResource::collection(
        CorporationStructure::with('info', 'type', 'services', 'solar_system')
            ->where('corporation_id', $CORP)->paginate()));

record('corporation.wallet-journal', "/api/v2/corporation/wallet-journal/$CORP", fn ($r) =>
    JsonResource::collection(
        CorporationWalletJournal::with('first_party', 'second_party')->where('corporation_id', $CORP)->paginate()));

record('corporation.wallet-transactions', "/api/v2/corporation/wallet-transactions/$CORP", fn ($r) =>
    JsonResource::collection(
        CorporationWalletTransaction::with('party', 'type')->where('corporation_id', $CORP)->paginate()));

// ── KillmailsController ──────────────────────────────────────────────────
record('killmails.detail', '/api/v2/killmails/15001', fn ($r) =>
    new KillmailDetailResource(KillmailDetail::with('attackers', 'victim')->findOrFail(15001)));

record('killmails.character', "/api/v2/character/killmails/$CHAR", fn ($r) =>
    JsonResource::collection(
        Killmail::with('detail', 'victim', 'attackers')
            ->whereHas('victim', fn ($q) => $q->where('character_id', $CHAR))
            ->orWhereHas('attackers', fn ($q) => $q->where('character_id', $CHAR))
            ->paginate()->appends($r->except('page'))));

record('killmails.corporation', "/api/v2/corporation/killmails/$CORP", fn ($r) =>
    JsonResource::collection(
        Killmail::whereHas('victim', fn ($q) => $q->where('corporation_id', $CORP))
            ->orWhereHas('attackers', fn ($q) => $q->where('corporation_id', $CORP))
            ->paginate()->appends($r->except('page'))));

// ── SquadController ──────────────────────────────────────────────────────
record('squads.index', '/api/v2/squads', fn ($r) =>
    SquadResource::collection(Squad::paginate()->appends($r->except('page'))));

record('squads.show', '/api/v2/squads/1', fn ($r) =>
    SquadResource::make(Squad::with('roles', 'moderators', 'members', 'applications')->findOrFail(1)));

// ── UserController ───────────────────────────────────────────────────────
record('users.index', '/api/v2/users', fn ($r) =>
    UserResource::collection(User::paginate()->appends($r->except('page'))));

record('users.show', '/api/v2/users/1', fn ($r) =>
    UserResource::make(User::findOrFail(1)));

file_put_contents(OUT . '/MANIFEST.json', json_encode($manifest, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n");
fwrite(STDERR, sprintf("\nrecorded %d responses\n", count($manifest)));
